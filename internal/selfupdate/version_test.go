package selfupdate

import "testing"

func TestParseVersion(t *testing.T) {
	cases := []struct {
		in      string
		want    version
		wantErr bool
	}{
		{in: "v0.1.1", want: version{0, 1, 1, ""}},
		{in: "0.1.1", want: version{0, 1, 1, ""}},
		{in: "v1.2.3", want: version{1, 2, 3, ""}},
		{in: "  v2.0.0  ", want: version{2, 0, 0, ""}},
		{in: "v1.0.0-rc.1", want: version{1, 0, 0, "rc.1"}},
		{in: "1.0.0-alpha", want: version{1, 0, 0, "alpha"}},
		{in: "v1.2.3+build.5", want: version{1, 2, 3, ""}},
		{in: "v1.0.0-rc.1+meta", want: version{1, 0, 0, "rc.1"}},
		{in: "v1", want: version{1, 0, 0, ""}},
		{in: "v1.2", want: version{1, 2, 0, ""}},
		{in: "", wantErr: true},
		{in: "v", wantErr: true},
		{in: "vx.y.z", wantErr: true},
		{in: "1.2.3.4", wantErr: true},
		{in: "v-1.0.0", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := parseVersion(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("parseVersion(%q): err=%v wantErr=%v", tc.in, err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if got != tc.want {
				t.Errorf("parseVersion(%q) = %+v, want %+v", tc.in, got, tc.want)
			}
		})
	}
}

func TestVersionCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v0.1.1", "v0.1.1", 0},
		{"0.1.1", "v0.1.1", 0}, // with/without v prefix are equal
		{"v0.2.0", "v0.1.1", 1},
		{"v0.1.1", "v0.2.0", -1},
		{"v1.0.0", "v0.9.9", 1},
		{"v1.2.0", "v1.1.9", 1},
		{"v1.0.1", "v1.0.0", 1},
		// prerelease ordering: a prerelease is lower than its stable release.
		{"v1.0.0-rc.1", "v1.0.0", -1},
		{"v1.0.0", "v1.0.0-rc.1", 1},
		{"v1.0.0-alpha", "v1.0.0-beta", -1},
		{"v1.0.0-alpha.1", "v1.0.0-alpha.2", -1},
		{"v1.0.0-alpha.1", "v1.0.0-alpha", 1}, // more identifiers > fewer
		{"v1.0.0-rc.2", "v1.0.0-rc.10", -1},   // numeric identifiers compare numerically
		{"v1.0.0-1", "v1.0.0-alpha", -1},      // numeric ranks below alphanumeric
		{"v1.0.0-rc.1", "v1.0.0-rc.1", 0},
	}
	for _, tc := range cases {
		t.Run(tc.a+"_vs_"+tc.b, func(t *testing.T) {
			av, err := parseVersion(tc.a)
			if err != nil {
				t.Fatalf("parse %q: %v", tc.a, err)
			}
			bv, err := parseVersion(tc.b)
			if err != nil {
				t.Fatalf("parse %q: %v", tc.b, err)
			}
			if got := av.compare(bv); got != tc.want {
				t.Errorf("compare(%q,%q) = %d, want %d", tc.a, tc.b, got, tc.want)
			}
			// Comparison must be antisymmetric.
			if got := bv.compare(av); got != -tc.want {
				t.Errorf("compare(%q,%q) = %d, want %d", tc.b, tc.a, got, -tc.want)
			}
		})
	}
}
