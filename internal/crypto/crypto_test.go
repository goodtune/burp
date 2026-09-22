package crypto

import (
	"strings"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	enc, err := NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	for _, plain := range []string{"", "ghu_abc123", strings.Repeat("x", 4096)} {
		t.Run(plain[:min(len(plain), 8)], func(t *testing.T) {
			ct, err := enc.Encrypt(plain)
			if err != nil {
				t.Fatal(err)
			}
			if ct == plain && plain != "" {
				t.Fatal("ciphertext equals plaintext")
			}
			got, err := enc.Decrypt(ct)
			if err != nil {
				t.Fatal(err)
			}
			if got != plain {
				t.Fatalf("got %q want %q", got, plain)
			}
		})
	}
}

func TestNoncesDiffer(t *testing.T) {
	key, _ := GenerateKey()
	enc, _ := NewEncryptor(key)
	a, _ := enc.Encrypt("same")
	b, _ := enc.Encrypt("same")
	if a == b {
		t.Fatal("two encryptions of the same plaintext produced identical ciphertext")
	}
}

func TestBadKey(t *testing.T) {
	tests := map[string]string{
		"not hex":   "zz",
		"too short": "00ff",
		"31 bytes":  strings.Repeat("ab", 31),
	}
	for name, key := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := NewEncryptor(key); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestDecryptErrors(t *testing.T) {
	key, _ := GenerateKey()
	enc, _ := NewEncryptor(key)
	if _, err := enc.Decrypt("!!!"); err == nil {
		t.Fatal("expected base64 error")
	}
	if _, err := enc.Decrypt("AAAA"); err == nil {
		t.Fatal("expected short ciphertext error")
	}
	other, _ := GenerateKey()
	enc2, _ := NewEncryptor(other)
	ct, _ := enc.Encrypt("secret")
	if _, err := enc2.Decrypt(ct); err == nil {
		t.Fatal("expected auth failure with wrong key")
	}
}

func TestPassthrough(t *testing.T) {
	p := NewPassthroughEncryptor()
	if !p.Passthrough() {
		t.Fatal("expected passthrough")
	}
	ct, _ := p.Encrypt("plain")
	if ct != "plain" {
		t.Fatalf("got %q", ct)
	}
	pt, _ := p.Decrypt("plain")
	if pt != "plain" {
		t.Fatalf("got %q", pt)
	}
}
