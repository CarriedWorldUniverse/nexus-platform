package bundle

import "testing"

func TestParseAssetName(t *testing.T) {
	cases := []struct {
		name         string
		wantBinary   string
		wantOS       string
		wantArch     string
		wantOK       bool
	}{
		{"nexus_0.2.0_linux_amd64.tar.gz", "nexus", "linux", "amd64", true},
		{"agentfunnel_0.2.0_darwin_arm64.tar.gz", "agentfunnel", "darwin", "arm64", true},
		{"nexus-jira-mcp_0.2.0_windows_amd64.tar.gz", "nexus-jira-mcp", "windows", "amd64", true},
		{"checksums.txt", "", "", "", false},
		{"ledger-source-0.1.3.tar.gz", "", "", "", false},
		{"random.zip", "", "", "", false},
		{"nexus_0.2.0_freebsd_amd64.tar.gz", "", "", "", false}, // unsupported OS
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			binary, os, arch, ok := parseAssetName(c.name)
			if ok != c.wantOK {
				t.Errorf("ok = %v, want %v", ok, c.wantOK)
			}
			if !ok {
				return
			}
			if binary != c.wantBinary || os != c.wantOS || arch != c.wantArch {
				t.Errorf("got (%q, %q, %q); want (%q, %q, %q)",
					binary, os, arch, c.wantBinary, c.wantOS, c.wantArch)
			}
		})
	}
}

func TestTargetString(t *testing.T) {
	tt := Target{OS: "linux", Arch: "amd64"}
	if tt.String() != "linux-amd64" {
		t.Errorf("Target.String() = %q", tt.String())
	}
}
