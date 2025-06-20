package utils

import (
	"testing"
)

func TestEncryptDecryptPassword(t *testing.T) {
	tests := []struct {
		name     string
		password string
		key      string
		wantErr  bool
	}{
		{
			name:     "valid 16-byte key",
			password: "testpassword123",
			key:      "1234567890123456", // 16 bytes
			wantErr:  false,
		},
		{
			name:     "valid 24-byte key",
			password: "testpassword123",
			key:      "123456789012345678901234", // 24 bytes
			wantErr:  false,
		},
		{
			name:     "valid 32-byte key",
			password: "testpassword123",
			key:      "12345678901234567890123456789012", // 32 bytes
			wantErr:  false,
		},
		{
			name:     "empty password",
			password: "",
			key:      "1234567890123456",
			wantErr:  false,
		},
		{
			name:     "unicode password",
			password: "пароль123",
			key:      "1234567890123456",
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encrypted, err := EncryptPassword(tt.password, tt.key)
			if (err != nil) != tt.wantErr {
				t.Errorf("EncryptPassword() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				// Test decryption
				decrypted, err := DecryptPassword(encrypted, tt.key)
				if err != nil {
					t.Errorf("DecryptPassword() error = %v", err)
					return
				}

				if decrypted != tt.password {
					t.Errorf("DecryptPassword() = %v, want %v", decrypted, tt.password)
				}

				// Verify encrypted is different from original
				if encrypted == tt.password {
					t.Error("EncryptPassword() returned same as input")
				}

				// Verify encrypted is not empty
				if encrypted == "" {
					t.Error("EncryptPassword() returned empty string")
				}
			}
		})
	}
}

func TestEncryptPasswordInvalidKey(t *testing.T) {
	tests := []struct {
		name string
		key  string
	}{
		{"too short", "short"},
		{"15 bytes", "123456789012345"},
		{"17 bytes", "12345678901234567"},
		{"23 bytes", "12345678901234567890123"},
		{"25 bytes", "1234567890123456789012345"},
		{"31 bytes", "1234567890123456789012345678901"},
		{"33 bytes", "123456789012345678901234567890123"},
		{"empty key", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := EncryptPassword("test", tt.key)
			if err != ErrInvalidKeySize {
				t.Errorf("EncryptPassword() error = %v, want %v", err, ErrInvalidKeySize)
			}
		})
	}
}

func TestDecryptPasswordInvalidKey(t *testing.T) {
	// First, create a valid encrypted password
	validKey := "1234567890123456"
	encrypted, err := EncryptPassword("test", validKey)
	if err != nil {
		t.Fatal("Failed to create test encrypted password:", err)
	}

	tests := []struct {
		name string
		key  string
	}{
		{"too short", "short"},
		{"15 bytes", "123456789012345"},
		{"17 bytes", "12345678901234567"},
		{"empty key", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DecryptPassword(encrypted, tt.key)
			if err != ErrInvalidKeySize {
				t.Errorf("DecryptPassword() error = %v, want %v", err, ErrInvalidKeySize)
			}
		})
	}
}

func TestDecryptPasswordInvalidData(t *testing.T) {
	key := "1234567890123456"

	tests := []struct {
		name      string
		encrypted string
	}{
		{"empty data", ""},
		{"invalid base64", "not-base64!@#"},
		{"too short ciphertext", "YWJj"},    // "abc" in base64
		{"random data", "cmFuZG9tZGF0YQ=="}, // "randomdata" in base64
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DecryptPassword(tt.encrypted, key)
			if err == nil {
				t.Error("DecryptPassword() expected error but got none")
			}
		})
	}
}

func TestEncryptPasswordDifferentResults(t *testing.T) {
	password := "testpassword"
	key := "1234567890123456"

	// Encrypt the same password multiple times
	encrypted1, err := EncryptPassword(password, key)
	if err != nil {
		t.Fatal("First encryption failed:", err)
	}

	encrypted2, err := EncryptPassword(password, key)
	if err != nil {
		t.Fatal("Second encryption failed:", err)
	}

	// Results should be different due to random nonce
	if encrypted1 == encrypted2 {
		t.Error("Multiple encryptions of same password should produce different results")
	}

	// Both should decrypt to the same original password
	decrypted1, err := DecryptPassword(encrypted1, key)
	if err != nil {
		t.Fatal("First decryption failed:", err)
	}

	decrypted2, err := DecryptPassword(encrypted2, key)
	if err != nil {
		t.Fatal("Second decryption failed:", err)
	}

	if decrypted1 != password || decrypted2 != password {
		t.Error("Decrypted passwords don't match original")
	}
}
