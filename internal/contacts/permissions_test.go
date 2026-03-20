package contacts

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParsePermission(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    Permission
		wantErr bool
	}{
		{"full", "rwx", Permission{true, true, true}, false},
		{"read-only", "r--", Permission{true, false, false}, false},
		{"read-write", "rw-", Permission{true, true, false}, false},
		{"none", "---", Permission{false, false, false}, false},
		{"read-execute", "r-x", Permission{true, false, true}, false},
		{"uppercase", "RWX", Permission{true, true, true}, false},
		{"too-short", "rw", Permission{}, true},
		{"too-long", "rwxx", Permission{}, true},
		{"invalid-char", "abc", Permission{}, true},
		{"invalid-read", "x--", Permission{}, true},
		{"invalid-write", "ra-", Permission{}, true},
		{"invalid-execute", "rwa", Permission{}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParsePermission(tc.input)
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.want, got)
			}
		})
	}
}

func TestPermission_String(t *testing.T) {
	tests := []struct {
		perm Permission
		want string
	}{
		{Permission{true, true, true}, "rwx"},
		{Permission{true, false, false}, "r--"},
		{Permission{false, false, false}, "---"},
		{Permission{true, true, false}, "rw-"},
		{Permission{true, false, true}, "r-x"},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, tc.perm.String())
	}
}

func TestCheckPermission_Explicit(t *testing.T) {
	c := Contact{
		Name:  "Eric",
		Email: "eric@test.com",
		Permissions: map[string]string{
			"claude@punt-labs.com": "rw-",
		},
	}
	perm := CheckPermission(c, "claude@punt-labs.com")
	assert.Equal(t, Permission{true, true, false}, perm)
}

func TestCheckPermission_Default(t *testing.T) {
	c := Contact{Name: "Unknown", Email: "unknown@test.com"}
	perm := CheckPermission(c, "claude@punt-labs.com")
	assert.Equal(t, Permission{}, perm)
}

func TestCheckPermission_NilMap(t *testing.T) {
	c := Contact{Name: "Test", Email: "test@test.com", Permissions: nil}
	perm := CheckPermission(c, "claude@punt-labs.com")
	assert.Equal(t, "---", perm.String())
}

func TestCheckPermission_MalformedFallsToDefault(t *testing.T) {
	c := Contact{
		Name:  "Bad",
		Email: "bad@test.com",
		Permissions: map[string]string{
			"claude@punt-labs.com": "invalid",
		},
	}
	perm := CheckPermission(c, "claude@punt-labs.com")
	assert.Equal(t, "---", perm.String())
}

func TestParsePermission_Roundtrip(t *testing.T) {
	inputs := []string{"rwx", "r--", "rw-", "r-x", "---", "-w-", "--x", "-wx"}
	for _, s := range inputs {
		p, err := ParsePermission(s)
		require.NoError(t, err)
		assert.Equal(t, s, p.String())
	}
}
