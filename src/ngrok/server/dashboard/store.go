package dashboard

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// Store persists dashboard users, tunnels and port mappings as a single
// JSON file. It is the source of truth for managed clients; the running
// ngrokd reconciles tunnels towards whatever is stored here.

type User struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	PassHash  string    `json:"pass_hash"` // pbkdf2$sha256$iter$salt$hash
	Role      string    `json:"role"`      // "admin" | "user"
	CreatedAt time.Time `json:"created_at"`
}

type Mapping struct {
	ID         string `json:"id"`
	Proto      string `json:"proto"` // tcp | http | https
	LocalIP    string `json:"local_ip"`
	LocalPort  int    `json:"local_port"`
	RemotePort int    `json:"remote_port"` // tcp only; 0 = auto
	Subdomain  string `json:"subdomain"`   // http/https only
	Note       string `json:"note"`
}

type Tunnel struct {
	ID                 string     `json:"id"`
	Key                string     `json:"key"` // "ngk-" 形态随机密钥, 客户端认证凭据
	Name               string     `json:"name"`
	Note               string     `json:"note"`
	OwnerID            string     `json:"owner_id"`
	Locked             bool       `json:"locked"`
	AllowRemoteTargets bool       `json:"allow_remote_targets,omitempty"` // 是否允许转发非本地回环目标 (默认 false)
	Node               string     `json:"node"`                           // 预留多节点; 当前=服务器域名
	CreatedAt          time.Time  `json:"created_at"`
	Mappings           []*Mapping `json:"mappings"`
}

// ApiKey 授予持有者对其归属用户隧道资源的只读访问权 (资源列表 + SKILL
// 文档), 供 AI agent 使用; 不含任何创建/修改能力。
type ApiKey struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Key        string     `json:"key"` // "onk-" 形态随机密钥
	OwnerID    string     `json:"owner_id"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

type settings struct {
	SessionSecret string `json:"session_secret"`
}

type storeFile struct {
	Settings settings          `json:"settings"`
	Users    []*User           `json:"users"`
	Tunnels  []*Tunnel         `json:"tunnels"`
	ApiKeys  []*ApiKey         `json:"api_keys,omitempty"`
}

type Store struct {
	mu   sync.RWMutex
	path string
	data storeFile
}

// ---------- id / key generation ----------

const idAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

func randBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}

// NewTunnelID returns a 10-char id like "Xw2dophvFo".
func NewTunnelID() string {
	b := randBytes(10)
	out := make([]byte, 10)
	for i, c := range b {
		out[i] = idAlphabet[int(c)%len(idAlphabet)]
	}
	return string(out)
}

// NewMappingID returns an 8-char hex id (also used as ReqTunnel.ReqId).
func NewMappingID() string {
	return hex.EncodeToString(randBytes(4))
}

// FormatKey renders 16 random bytes as ngk-xxxxxxxx-xxxxxxxx-xxxxxxxx-xxxxxxxx,
// the same shape as the client's machine key.
func FormatKey(raw []byte) string {
	h := hex.EncodeToString(raw)
	return "ngk-" + h[0:8] + "-" + h[8:16] + "-" + h[16:24] + "-" + h[24:32]
}

func NewKey() string { return FormatKey(randBytes(16)) }

// ---------- store lifecycle ----------

func OpenStore(path string) (*Store, error) {
	s := &Store{path: path}
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(b, &s.data); err != nil {
			return nil, fmt.Errorf("corrupt dashboard data file %s: %v", path, err)
		}
	case os.IsNotExist(err):
		// fresh install
	default:
		return nil, err
	}

	if s.data.Settings.SessionSecret == "" {
		s.data.Settings.SessionSecret = hex.EncodeToString(randBytes(32))
	}
	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	return s, nil
}

// saveLocked writes the file atomically. Callers must hold mu for writing
// (or be in single-threaded startup).
func (s *Store) saveLocked() error {
	b, err := json.MarshalIndent(&s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.MkdirAll(dirOf(s.path), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func dirOf(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[:i]
		}
	}
	return "."
}

func (s *Store) SessionSecret() []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return []byte(s.data.Settings.SessionSecret)
}

// ---------- users ----------

// BootstrapAdmin creates the initial admin account if the store has no
// users. Returns (username, password, created).
func (s *Store) BootstrapAdmin(password string) (string, string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.data.Users) > 0 {
		return "", "", false
	}
	pass := password
	if pass == "" {
		pass = randPassword()
	}
	u := &User{
		ID:        NewMappingID() + NewMappingID(),
		Username:  "admin",
		PassHash:  HashPassword(pass),
		Role:      "admin",
		CreatedAt: time.Now(),
	}
	s.data.Users = append(s.data.Users, u)
	if err := s.saveLocked(); err != nil {
		return "", "", false
	}
	return u.Username, pass, true
}

func randPassword() string {
	// 12 chars from a confusion-free alphabet
	const alpha = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKMNPQRSTUVWXYZ23456789"
	b := randBytes(12)
	out := make([]byte, 12)
	for i, c := range b {
		out[i] = alpha[int(c)%len(alpha)]
	}
	return string(out)
}

func (s *Store) Users() []*User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*User, len(s.data.Users))
	copy(out, s.data.Users)
	return out
}

func (s *Store) UserByID(id string) *User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, u := range s.data.Users {
		if u.ID == id {
			return u
		}
	}
	return nil
}

func (s *Store) UserByName(username string) *User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, u := range s.data.Users {
		if u.Username == username {
			return u
		}
	}
	return nil
}

func (s *Store) CreateUser(username, password, role string) (*User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.data.Users {
		if u.Username == username {
			return nil, fmt.Errorf("用户名已存在")
		}
	}
	if role != "admin" && role != "user" {
		return nil, fmt.Errorf("无效角色 %q", role)
	}
	u := &User{
		ID:        NewMappingID() + NewMappingID(),
		Username:  username,
		PassHash:  HashPassword(password),
		Role:      role,
		CreatedAt: time.Now(),
	}
	s.data.Users = append(s.data.Users, u)
	return u, s.saveLocked()
}

func (s *Store) UpdateUserPassword(id, password string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.data.Users {
		if u.ID == id {
			u.PassHash = HashPassword(password)
			return s.saveLocked()
		}
	}
	return fmt.Errorf("用户不存在")
}

func (s *Store) UpdateUserRole(id, role string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if role != "admin" && role != "user" {
		return fmt.Errorf("无效角色 %q", role)
	}
	for _, u := range s.data.Users {
		if u.ID == id {
			u.Role = role
			return s.saveLocked()
		}
	}
	return fmt.Errorf("用户不存在")
}

func (s *Store) DeleteUser(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, u := range s.data.Users {
		if u.ID == id {
			if u.Role == "admin" && s.adminCountLocked() <= 1 {
				return fmt.Errorf("不能删除最后一个管理员")
			}
			s.data.Users = append(s.data.Users[:i:i], s.data.Users[i+1:]...)
			// tunnels owned by the deleted user fall back to no owner but
			// remain visible to admins
			return s.saveLocked()
		}
	}
	return fmt.Errorf("用户不存在")
}

func (s *Store) adminCountLocked() int {
	n := 0
	for _, u := range s.data.Users {
		if u.Role == "admin" {
			n++
		}
	}
	return n
}

// ---------- tunnels ----------

// Tunnels returns tunnels visible to the given user (nil user = all, admin).
func (s *Store) Tunnels(ownerUserID string, admin bool) []*Tunnel {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Tunnel, 0, len(s.data.Tunnels))
	for _, t := range s.data.Tunnels {
		if admin || t.OwnerID == ownerUserID {
			out = append(out, t)
		}
	}
	return out
}

func (s *Store) TunnelByID(id string) *Tunnel {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.tunnelByIDLocked(id)
}

func (s *Store) tunnelByIDLocked(id string) *Tunnel {
	for _, t := range s.data.Tunnels {
		if t.ID == id {
			return t
		}
	}
	return nil
}

// keyMatches reports whether the presented key belongs to this tunnel
// (constant-time per candidate).
func (t *Tunnel) keyMatches(key string) bool {
	return subtle.ConstantTimeCompare([]byte(t.Key), []byte(key)) == 1
}

// AuthenticateKey validates a client-presented key against all tunnels.
// Returns the owning tunnel. Comparison is constant-time per candidate.
func (s *Store) AuthenticateKey(key string) (*Tunnel, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, t := range s.data.Tunnels {
		if t.keyMatches(key) {
			return t, true
		}
	}
	return nil, false
}

type NewTunnelInput struct {
	Name    string
	Note    string
	OwnerID string
}

func (s *Store) CreateTunnel(in NewTunnelInput) *Tunnel {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := &Tunnel{
		ID:        NewTunnelID(),
		Key:       NewKey(),
		Name:      in.Name,
		Note:      in.Note,
		OwnerID:   in.OwnerID,
		Node:      "",
		CreatedAt: time.Now(),
		Mappings:  []*Mapping{},
	}
	s.data.Tunnels = append(s.data.Tunnels, t)
	if err := s.saveLocked(); err != nil {
		return nil
	}
	return t
}

func (s *Store) UpdateTunnelMeta(id, name, note string, locked, allowRemoteTargets bool, ownerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.tunnelByIDLocked(id)
	if t == nil {
		return fmt.Errorf("隧道不存在")
	}
	t.Name = name
	t.Note = note
	t.Locked = locked
	t.AllowRemoteTargets = allowRemoteTargets
	t.OwnerID = ownerID
	return s.saveLocked()
}

func (s *Store) ResetKey(id string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.tunnelByIDLocked(id)
	if t == nil {
		return "", fmt.Errorf("隧道不存在")
	}
	t.Key = NewKey()
	return t.Key, s.saveLocked()
}

func (s *Store) DeleteTunnel(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, t := range s.data.Tunnels {
		if t.ID == id {
			s.data.Tunnels = append(s.data.Tunnels[:i:i], s.data.Tunnels[i+1:]...)
			return s.saveLocked()
		}
	}
	return fmt.Errorf("隧道不存在")
}

// ---------- mappings ----------

type MappingInput struct {
	Proto      string
	LocalIP    string
	LocalPort  int
	RemotePort int
	Subdomain  string
	Note       string
}

func isLoopbackHost(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "" || h == "127.0.0.1" || h == "localhost" || h == "::1" {
		return true
	}
	if strings.HasPrefix(h, "127.") {
		return true
	}
	return false
}

func validateMappingInput(in *MappingInput, allowRemote bool) error {
	switch in.Proto {
	case "tcp":
	case "http", "https":
	default:
		return fmt.Errorf("协议必须是 tcp/http/https")
	}
	if in.LocalPort <= 0 || in.LocalPort > 65535 {
		return fmt.Errorf("本地端口无效")
	}
	if in.RemotePort < 0 || in.RemotePort > 65535 {
		return fmt.Errorf("公网端口无效")
	}
	if in.RemotePort > 0 && in.RemotePort < 1024 {
		return fmt.Errorf("公网端口不允许使用系统特权端口 (< 1024)")
	}
	if in.LocalIP == "" {
		in.LocalIP = "127.0.0.1"
	}
	if !allowRemote && !isLoopbackHost(in.LocalIP) {
		return fmt.Errorf("默认仅允许转发 127.0.0.1 / localhost 本地服务; 如需转发局域网目标, 请在隧道配置中开启「允许远程目标」")
	}
	if in.Subdomain != "" {
		sub := strings.ToLower(strings.TrimSpace(in.Subdomain))
		for _, c := range sub {
			if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') {
				return fmt.Errorf("子域名仅允许小写字母、数字和中划线")
			}
		}
		in.Subdomain = sub
	}
	return nil
}

func (s *Store) checkSubdomainConflictLocked(tunnelID, subdomain string) error {
	if subdomain == "" {
		return nil
	}
	targetTunnel := s.tunnelByIDLocked(tunnelID)
	for _, t := range s.data.Tunnels {
		for _, m := range t.Mappings {
			if strings.EqualFold(m.Subdomain, subdomain) {
				// 同一用户名下的隧道允许迁移/复用, 不同用户禁止抢占
				if targetTunnel != nil && t.OwnerID != targetTunnel.OwnerID {
					return fmt.Errorf("子域名 %q 已被其他用户占用", subdomain)
				}
			}
		}
	}
	return nil
}

func (s *Store) AddMapping(tunnelID string, in MappingInput) (*Mapping, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.tunnelByIDLocked(tunnelID)
	if t == nil {
		return nil, fmt.Errorf("隧道不存在")
	}
	if len(t.Mappings) >= 30 {
		return nil, fmt.Errorf("单条隧道最多添加 30 个端口映射")
	}
	if err := validateMappingInput(&in, t.AllowRemoteTargets); err != nil {
		return nil, err
	}
	if err := s.checkSubdomainConflictLocked(tunnelID, in.Subdomain); err != nil {
		return nil, err
	}
	m := &Mapping{
		ID:         NewMappingID(),
		Proto:      in.Proto,
		LocalIP:    in.LocalIP,
		LocalPort:  in.LocalPort,
		RemotePort: in.RemotePort,
		Subdomain:  in.Subdomain,
		Note:       in.Note,
	}
	t.Mappings = append(t.Mappings, m)
	return m, s.saveLocked()
}

func (s *Store) UpdateMapping(tunnelID, mappingID string, in MappingInput) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.tunnelByIDLocked(tunnelID)
	if t == nil {
		return fmt.Errorf("隧道不存在")
	}
	if err := validateMappingInput(&in, t.AllowRemoteTargets); err != nil {
		return err
	}
	if err := s.checkSubdomainConflictLocked(tunnelID, in.Subdomain); err != nil {
		return err
	}
	for _, m := range t.Mappings {
		if m.ID == mappingID {
			m.Proto = in.Proto
			m.LocalIP = in.LocalIP
			m.LocalPort = in.LocalPort
			m.RemotePort = in.RemotePort
			m.Subdomain = in.Subdomain
			m.Note = in.Note
			return s.saveLocked()
		}
	}
	return fmt.Errorf("端口映射不存在")
}

func (s *Store) DeleteMapping(tunnelID, mappingID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.tunnelByIDLocked(tunnelID)
	if t == nil {
		return fmt.Errorf("隧道不存在")
	}
	for i, m := range t.Mappings {
		if m.ID == mappingID {
			t.Mappings = append(t.Mappings[:i:i], t.Mappings[i+1:]...)
			return s.saveLocked()
		}
	}
	return fmt.Errorf("端口映射不存在")
}

// MapppingByID finds a mapping across all tunnels (used by PATCH /api/mappings/{mid}).
func (s *Store) MappingByID(mappingID string) (*Tunnel, *Mapping) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, t := range s.data.Tunnels {
		for _, m := range t.Mappings {
			if m.ID == mappingID {
				return t, m
			}
		}
	}
	return nil, nil
}

// ---------- api keys ----------

func NewApiKeySecret() string {
	return "onk-" + hex.EncodeToString(randBytes(20))
}

// ApiKeys returns api keys visible to the given user (owner-scoped; admin
// sees all).
func (s *Store) ApiKeys(ownerUserID string, admin bool) []*ApiKey {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*ApiKey, 0, len(s.data.ApiKeys))
	for _, k := range s.data.ApiKeys {
		if admin || k.OwnerID == ownerUserID {
			out = append(out, k)
		}
	}
	return out
}

func (s *Store) ApiKeyByID(id string) *ApiKey {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, k := range s.data.ApiKeys {
		if k.ID == id {
			return k
		}
	}
	return nil
}

func (s *Store) CreateApiKey(ownerUserID, name string) *ApiKey {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := &ApiKey{
		ID:        NewMappingID() + NewMappingID(),
		Name:      name,
		Key:       NewApiKeySecret(),
		OwnerID:   ownerUserID,
		CreatedAt: time.Now(),
	}
	s.data.ApiKeys = append(s.data.ApiKeys, k)
	if err := s.saveLocked(); err != nil {
		return nil
	}
	return k
}

func (s *Store) DeleteApiKey(id, ownerUserID string, admin bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, k := range s.data.ApiKeys {
		if k.ID == id && (admin || k.OwnerID == ownerUserID) {
			s.data.ApiKeys = append(s.data.ApiKeys[:i:i], s.data.ApiKeys[i+1:]...)
			return s.saveLocked()
		}
	}
	return fmt.Errorf("API KEY 不存在")
}

// AuthenticateApiKey validates a presented key and returns its owner user.
// Comparison is constant-time per candidate.
func (s *Store) AuthenticateApiKey(key string) (*ApiKey, *User, bool) {
	if key == "" {
		return nil, nil, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, k := range s.data.ApiKeys {
		if subtle.ConstantTimeCompare([]byte(k.Key), []byte(key)) == 1 {
			for _, u := range s.data.Users {
				if u.ID == k.OwnerID {
					return k, u, true
				}
			}
			return nil, nil, false
		}
	}
	return nil, nil, false
}

// TouchApiKey records last-used time (best effort, throttled to once a
// minute to avoid store rewrites on every AI request).
func (s *Store) TouchApiKey(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, k := range s.data.ApiKeys {
		if k.ID == id {
			if k.LastUsedAt == nil || time.Since(*k.LastUsedAt) >= time.Minute {
				now := time.Now()
				k.LastUsedAt = &now
				_ = s.saveLocked()
			}
			return
		}
	}
}
