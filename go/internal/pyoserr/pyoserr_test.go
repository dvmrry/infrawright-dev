package pyoserr

import (
	"errors"
	"io/fs"
	"testing"
)

// TestMissing pins node-src/io/terraform-command.ts at
// f3a86f2d24dddd4ebf95362d55718a81137800f2:307-310. That source escapes
// original backslashes first, then apostrophes, leaves every other code point
// unchanged, and wraps the result in the fixed English Errno 2 diagnostic.
func TestMissing(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "plain",
			path: "/missing/terraform",
			want: `[Errno 2] No such file or directory: '/missing/terraform'`,
		},
		{
			name: "apostrophe",
			path: "/tmp/team's/terraform",
			want: `[Errno 2] No such file or directory: '/tmp/team\'s/terraform'`,
		},
		{
			name: "backslash",
			path: `C:\tools\terraform.exe`,
			want: `[Errno 2] No such file or directory: 'C:\\tools\\terraform.exe'`,
		},
		{
			name: "backslash_then_apostrophe",
			path: `C:\team's\terraform.exe`,
			want: `[Errno 2] No such file or directory: 'C:\\team\'s\\terraform.exe'`,
		},
		{
			name: "non_ascii_and_other_punctuation",
			path: `/工具/terraform-été"`,
			want: `[Errno 2] No such file or directory: '/工具/terraform-été"'`,
		},
		{
			name: "empty",
			path: "",
			want: `[Errno 2] No such file or directory: ''`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := Missing(test.path)
			if got := err.Error(); got != test.want {
				t.Errorf("Missing(%q).Error() = %q, want %q", test.path, got, test.want)
			}
			if !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("errors.Is(Missing(%q), fs.ErrNotExist) = false, want true", test.path)
			}
		})
	}
}
