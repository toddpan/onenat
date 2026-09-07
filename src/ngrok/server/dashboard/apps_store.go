package dashboard

// apps_store.go — Store persistence for applications and skill-file metadata.
// Skill file CONTENT lives on disk (skills.go); only metadata is stored here.

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// ---------- id generation ----------

// NewAppID returns a random "app-" prefixed id (not sequential → no enumeration).
func NewAppID() string { return "app-" + NewTunnelID() }

// NewSkillID returns a random "sk-" prefixed id; it doubles as the on-disk
// filename so user-supplied names never touch the filesystem.
func NewSkillID() string { return "sk-" + NewMappingID() + NewMappingID() }

// ---------- helpers ----------

func (s *Store) appByIDLocked(id string) *App {
	for _, a := range s.data.Apps {
		if a.ID == id {
			return a
		}
	}
	return nil
}

func (s *Store) skillByIDLocked(id string) *SkillFile {
	for _, sk := range s.data.Skills {
		if sk.ID == id {
			return sk
		}
	}
	return nil
}

// AppMappingCountLocked counts mappings bound to the app (delete protection).
func (s *Store) AppMappingCountLocked(appID string) int {
	n := 0
	for _, t := range s.data.Tunnels {
		for _, m := range t.Mappings {
			if m.AppID == appID {
				n++
			}
		}
	}
	return n
}

// AppMappingCount is the public wrapper (RLock).
func (s *Store) AppMappingCount(appID string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.AppMappingCountLocked(appID)
}

// ---------- app CRUD ----------

// Apps returns apps visible to the user (owner-scoped; admin sees all).
func (s *Store) Apps(ownerUserID string, admin bool) []*App {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*App, 0, len(s.data.Apps))
	for _, a := range s.data.Apps {
		if admin || a.OwnerID == ownerUserID {
			out = append(out, a)
		}
	}
	return out
}

func (s *Store) AppByID(id string) *App {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.appByIDLocked(id)
}

// appValidType checks the app type whitelist.
func appValidType(t string) bool {
	switch t {
	case "ssh", "http-api", "web", "database", "custom":
		return true
	}
	return false
}

func appValidAuthType(t string) bool {
	switch t {
	case "none", "basic", "bearer", "header", "custom":
		return true
	}
	return false
}

// AppAuthInput carries plaintext credentials into the store; the store seals
// them immediately. Plaintext never lands in the store file.
type AppAuthInput struct {
	AuthType     string            `json:"auth_type"`
	Username     string            `json:"username"`
	Password     string            `json:"password"` // plaintext in transit only (local HTTP/HTTPS to dashboard)
	ApiKey       string            `json:"api_key"`
	ExtraHeaders map[string]string `json:"extra_headers"`
}

// AppInput is the create/update payload for an application.
type AppInput struct {
	Name        string
	Type        string
	Description string
	OwnerID     string
	InternalURL string
	Auth        AppAuthInput
	Tags        []string
}

// validateAppInput normalizes and validates common fields (credentials are
// validated separately — metadata updates never touch auth).
func validateAppInput(in *AppInput) error {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len([]rune(in.Name)) > 64 {
		return fmt.Errorf("应用名称必填且不超过 64 字符")
	}
	if !appValidType(in.Type) {
		return fmt.Errorf("无效的应用类型 %q", in.Type)
	}
	if len(in.Description) > 500 {
		return fmt.Errorf("描述不超过 500 字符")
	}
	return nil
}

// validateAuthInput validates credential-bearing inputs (create / credential
// update).
func validateAuthInput(in *AppAuthInput) error {
	if !appValidAuthType(in.AuthType) {
		return fmt.Errorf("无效的认证类型 %q", in.AuthType)
	}
	if in.AuthType == "basic" && strings.TrimSpace(in.Username) == "" {
		return fmt.Errorf("basic 认证需要用户名")
	}
	return nil
}

// CreateApp creates an app with sealed credentials. Requires the master key
// to be injected (SetCredentialKey).
func (s *Store) CreateApp(in AppInput) (*App, error) {
	if err := validateAppInput(&in); err != nil {
		return nil, err
	}
	if err := validateAuthInput(&in.Auth); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.credKey == nil {
		return nil, fmt.Errorf("凭证主密钥未初始化")
	}
	auth, err := s.sealAuthLocked(in.Auth)
	if err != nil {
		return nil, err
	}
	a := &App{
		ID:          NewAppID(),
		Name:        strings.TrimSpace(in.Name),
		Type:        in.Type,
		Description: strings.TrimSpace(in.Description),
		OwnerID:     in.OwnerID,
		InternalURL: strings.TrimSpace(in.InternalURL),
		Auth:        auth,
		Tags:        in.Tags,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	s.data.Apps = append(s.data.Apps, a)
	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	return a, nil
}

// sealAuthLocked encrypts credential fields; empty values stay empty
// (distinguishing "unset" from "empty string" credential).
func (s *Store) sealAuthLocked(in AppAuthInput) (AppAuth, error) {
	out := AppAuth{AuthType: in.AuthType, Username: strings.TrimSpace(in.Username)}
	var err error
	if out.PasswordEnc, err = SealSecret(s.credKey, in.Password); err != nil {
		return out, err
	}
	if out.ApiKeyEnc, err = SealSecret(s.credKey, in.ApiKey); err != nil {
		return out, err
	}
	if len(in.ExtraHeaders) > 0 {
		out.ExtraHeaders = map[string]string{}
		for k, v := range in.ExtraHeaders {
			k = strings.TrimSpace(k)
			if k == "" {
				continue
			}
			if out.ExtraHeaders[k], err = SealSecret(s.credKey, v); err != nil {
				return out, err
			}
		}
	}
	return out, nil
}

// UpdateAppMeta updates non-credential fields.
func (s *Store) UpdateAppMeta(id string, in AppInput) error {
	if err := validateAppInput(&in); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.appByIDLocked(id)
	if a == nil {
		return fmt.Errorf("应用不存在")
	}
	a.Name = strings.TrimSpace(in.Name)
	a.Type = in.Type
	a.Description = strings.TrimSpace(in.Description)
	a.InternalURL = strings.TrimSpace(in.InternalURL)
	a.Tags = in.Tags
	a.UpdatedAt = time.Now()
	return s.saveLocked()
}

// UpdateAppCredential replaces the upstream credentials (sealed at rest).
func (s *Store) UpdateAppCredential(id string, in AppAuthInput) error {
	if err := validateAuthInput(&in); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.credKey == nil {
		return fmt.Errorf("凭证主密钥未初始化")
	}
	a := s.appByIDLocked(id)
	if a == nil {
		return fmt.Errorf("应用不存在")
	}
	auth, err := s.sealAuthLocked(in)
	if err != nil {
		return err
	}
	a.Auth = auth
	a.UpdatedAt = time.Now()
	return s.saveLocked()
}

// DeleteApp removes an app (and its skills). When mappings still reference it,
// the caller must pass force=true to unbind them (mappings survive, app_id
// cleared to "" so legacy "未关联" flows apply).
func (s *Store) DeleteApp(id string, force bool) (unbound int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.appByIDLocked(id)
	if a == nil {
		return 0, fmt.Errorf("应用不存在")
	}
	n := s.AppMappingCountLocked(id)
	if n > 0 && !force {
		return n, fmt.Errorf("该应用仍被 %d 个端口映射绑定; 使用 force 解绑后删除", n)
	}
	for _, t := range s.data.Tunnels {
		for _, m := range t.Mappings {
			if m.AppID == id {
				m.AppID = ""
			}
		}
	}
	// drop skill metadata (content removed by the caller via SkillDiskPaths)
	s.data.Skills = removeIf(s.data.Skills, func(x *SkillFile) bool { return x.AppID == id })
	s.data.Apps = removeIf(s.data.Apps, func(x *App) bool { return x.ID == id })
	return n, s.saveLocked()
}

// removeIf filters a slice in place (3-index slice trick keeps it simple).
func removeIf[T any](xs []*T, pred func(*T) bool) []*T {
	out := xs[:0]
	for _, x := range xs {
		if !pred(x) {
			out = append(out, x)
		}
	}
	return out
}

// ---------- skill metadata ----------

// SkillFiles returns the skill metadata of one app, newest-updated first.
func (s *Store) SkillFiles(appID string) []*SkillFile {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []*SkillFile{}
	for _, sk := range s.data.Skills {
		if sk.AppID == appID {
			out = append(out, sk)
		}
	}
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].UpdatedAt.After(out[i].UpdatedAt) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func (s *Store) SkillByID(id string) *SkillFile {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.skillByIDLocked(id)
}

// SkillByAppName resolves (appID, display name) → skill, for AI-facing URLs.
func (s *Store) SkillByAppName(appID, name string) *SkillFile {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, sk := range s.data.Skills {
		if sk.AppID == appID && sk.Name == name {
			return sk
		}
	}
	return nil
}

// AddSkillMeta inserts metadata for a freshly written skill file. Caller has
// already written + validated the content (skills.go).
func (s *Store) AddSkillMeta(appID, name, ext string, content []byte, updatedBy string) (*SkillFile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.appByIDLocked(appID) == nil {
		return nil, fmt.Errorf("应用不存在")
	}
	n := 0
	for _, sk := range s.data.Skills {
		if sk.AppID == appID {
			n++
			if sk.Name == name {
				return nil, fmt.Errorf("同名技能文件已存在: %s", name)
			}
		}
	}
	if n >= maxSkillsPerApp {
		return nil, fmt.Errorf("单应用最多 %d 个技能文件", maxSkillsPerApp)
	}
	now := time.Now()
	sk := &SkillFile{
		ID:        NewSkillID(),
		AppID:     appID,
		Name:      name,
		Ext:       ext,
		Size:      int64(len(content)),
		SHA256:    sha256Hex(content),
		Version:   1,
		UpdatedBy: updatedBy,
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.data.Skills = append(s.data.Skills, sk)
	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	return sk, nil
}

// ReplaceSkillMeta updates metadata after a content rewrite (version bump).
func (s *Store) ReplaceSkillMeta(id string, content []byte, updatedBy string) (*SkillFile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sk := s.skillByIDLocked(id)
	if sk == nil {
		return nil, fmt.Errorf("技能文件不存在")
	}
	sk.Size = int64(len(content))
	sk.SHA256 = sha256Hex(content)
	sk.Version++
	sk.UpdatedBy = updatedBy
	sk.UpdatedAt = time.Now()
	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	return sk, nil
}

// DeleteSkillMeta removes metadata; returns the disk file name to delete.
func (s *Store) DeleteSkillMeta(id string) (appID, diskName string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sk := s.skillByIDLocked(id)
	if sk == nil {
		return "", "", fmt.Errorf("技能文件不存在")
	}
	appID, diskName = sk.AppID, sk.ID+sk.Ext
	s.data.Skills = removeIf(s.data.Skills, func(x *SkillFile) bool { return x.ID == id })
	return appID, diskName, s.saveLocked()
}

// AppSkillDiskPaths lists every on-disk skill file of an app (for app deletion).
func (s *Store) AppSkillDiskPaths(appID string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []string
	for _, sk := range s.data.Skills {
		if sk.AppID == appID {
			out = append(out, sk.ID+sk.Ext)
		}
	}
	return out
}

// ---------- credential reveal (audited paths call these) ----------

// RevealAppCredential decrypts the upstream credentials of an app.
// Callers must have verified ownership AND audited the access.
func (s *Store) RevealAppCredential(id string) (username, password, apiKey string, extra map[string]string, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.credKey == nil {
		return "", "", "", nil, fmt.Errorf("凭证主密钥未初始化")
	}
	a := s.appByIDLocked(id)
	if a == nil {
		return "", "", "", nil, fmt.Errorf("应用不存在")
	}
	return s.revealAuthLocked(&a.Auth)
}

// RevealMappingCredential 返回映射上游实例的有效凭证: 映射级覆盖优先,
// 否则回退绑定应用默认。source 标明凭证来源 ("mapping" | "app")。
func (s *Store) RevealMappingCredential(mappingID string) (authType, username, password, apiKey, source string, extra map[string]string, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.credKey == nil {
		err = fmt.Errorf("凭证主密钥未初始化")
		return
	}
	var m *Mapping
	for _, t := range s.data.Tunnels {
		for _, mm := range t.Mappings {
			if mm.ID == mappingID {
				m = mm
				break
			}
		}
		if m != nil {
			break
		}
	}
	if m == nil {
		err = fmt.Errorf("端口映射不存在")
		return
	}
	auth := m.Auth
	source = "mapping"
	if auth == nil {
		a := s.appByIDLocked(m.AppID)
		if a == nil {
			err = fmt.Errorf("映射未关联应用, 无可用凭证")
			return
		}
		auth = &a.Auth
		source = "app"
	}
	authType = auth.AuthType
	username, password, apiKey, extra, err = s.revealAuthLocked(auth)
	return
}

// revealAuthLocked 解封一份 AppAuth 密文; 调用方须持有读锁且已校验 credKey。
func (s *Store) revealAuthLocked(auth *AppAuth) (username, password, apiKey string, extra map[string]string, err error) {
	username = auth.Username
	if password, err = OpenSecret(s.credKey, auth.PasswordEnc); err != nil && err != errNotSealed {
		return "", "", "", nil, err
	}
	if apiKey, err = OpenSecret(s.credKey, auth.ApiKeyEnc); err != nil && err != errNotSealed {
		return "", "", "", nil, err
	}
	if len(auth.ExtraHeaders) > 0 {
		extra = map[string]string{}
		for k, v := range auth.ExtraHeaders {
			pv, err := OpenSecret(s.credKey, v)
			if err != nil && err != errNotSealed {
				return "", "", "", nil, err
			}
			extra[k] = pv
		}
	}
	return username, password, apiKey, extra, nil
}

// AppOwnedBy is the ownership check used by handlers.
func (s *Store) AppOwnedBy(appID, userID string, admin bool) bool {
	a := s.AppByID(appID)
	return a != nil && (admin || a.OwnerID == userID)
}

// ---------- compile-time interface sanity ----------

var _ = os.FileMode(0600) // keep os import when methods are trimmed
var _ sync.RWMutex
