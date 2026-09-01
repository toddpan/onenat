package dashboard

import "testing"

func TestApiKeyLifecycle(t *testing.T) {
	s := tempStore(t)
	s.BootstrapAdmin("adminpass1")
	u, _ := s.CreateUser("alice", "alicepass", "user")

	k := s.CreateApiKey(u.ID, "my-ai")
	if k == nil || k.Key == "" || len(k.ID) != 16 {
		t.Fatalf("api key must be created with id+key, got %+v", k)
	}
	if got := s.ApiKeys(u.ID, false); len(got) != 1 {
		t.Fatalf("owner listing = %d, want 1", len(got))
	}
	if got := s.ApiKeys("", true); len(got) != 1 {
		t.Fatalf("admin listing = %d, want 1", len(got))
	}

	// auth: valid key resolves to the owner user
	ak, u2, ok := s.AuthenticateApiKey(k.Key)
	if !ok || u2 == nil || u2.ID != u.ID || ak.ID != k.ID {
		t.Fatal("valid key must authenticate to its owner")
	}
	if _, _, ok := s.AuthenticateApiKey("onk-wrong"); ok {
		t.Fatal("wrong key must not authenticate")
	}
	if _, _, ok := s.AuthenticateApiKey(""); ok {
		t.Fatal("empty key must not authenticate")
	}

	// scoping: another user cannot see or delete alice's key
	other, _ := s.CreateUser("bob", "bobpass99", "user")
	if got := s.ApiKeys(other.ID, false); len(got) != 0 {
		t.Fatalf("other user must not see alice's keys, got %d", len(got))
	}
	if err := s.DeleteApiKey(k.ID, other.ID, false); err == nil {
		t.Fatal("other user must not delete alice's key")
	}

	// deletion: owner can delete own key
	if err := s.DeleteApiKey(k.ID, u.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := s.AuthenticateApiKey(k.Key); ok {
		t.Fatal("deleted key must not authenticate")
	}
}
