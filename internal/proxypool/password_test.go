package proxypool

import "testing"

func TestPasswordHashVerify(t *testing.T) {
	hash, err := HashPassword("secret")
	if err != nil {
		t.Fatal(err)
	}
	cfg := Settings{AdminPasswordHash: hash}
	if !VerifyPassword("secret", cfg) {
		t.Fatal("expected password to verify")
	}
	if VerifyPassword("wrong", cfg) {
		t.Fatal("unexpected password verification success")
	}
}

func TestPasswordLegacyVerify(t *testing.T) {
	cfg := Settings{AdminPassword: "legacy"}
	if !VerifyPassword("legacy", cfg) {
		t.Fatal("expected legacy password to verify")
	}
}
