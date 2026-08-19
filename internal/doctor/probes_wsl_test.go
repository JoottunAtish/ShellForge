package doctor

import (
	"context"
	"testing"
	"unicode/utf16"
)

func utf16leBytes(s string, bom bool) []byte {
	runes := utf16.Encode([]rune(s))
	out := []byte{}
	if bom {
		out = append(out, 0xFF, 0xFE)
	}
	for _, r := range runes {
		out = append(out, byte(r), byte(r>>8))
	}
	return out
}

// TestDecodeWSLTextHandlesUTF16LE covers the shapes wsl.exe output actually
// takes, plus the two malformed shapes that must not produce garbage.
func TestDecodeWSLTextHandlesUTF16LE(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{"utf16le with bom", utf16leBytes("NAME  STATE  VERSION", true), "NAME  STATE  VERSION"},
		{"utf16le without bom", utf16leBytes("NAME  STATE  VERSION", false), "NAME  STATE  VERSION"},
		{"utf8 passes through unchanged", []byte("healthy!"), "healthy!"},
		{"empty", nil, ""},
		{"odd length utf16-shaped is invalid", []byte{0x41, 0x00, 0x42}, ""},
		{"odd length utf8 passes through unchanged", []byte("Kernel version: 5.15.146.1\n"), "Kernel version: 5.15.146.1\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := decodeWSLText(tt.data); got != tt.want {
				t.Errorf("decodeWSLText(%v) = %q, want %q", tt.data, got, tt.want)
			}
		})
	}
}

func TestClassifyWSLListReadsTheVersionColumn(t *testing.T) {
	tests := []struct {
		name      string
		text      string
		wantLevel Level
		wantOther string
	}{
		{
			"one distribution at version 2",
			"  NAME      STATE     VERSION\n* Ubuntu     Running   2\n",
			OK, "",
		},
		{
			// The reviewer's own repro: the Shellforge sandbox distribution
			// is absent and a foreign WSL 1 distribution exists alongside
			// where it would go. That is not a blocker, since Shellforge
			// imports its own WSL 2 distribution regardless of what else is
			// installed, so this is Warn, naming the distribution, not Fail.
			"shellforge absent, a foreign distribution at version 1",
			"  NAME    STATE     VERSION\n* Ubuntu  Stopped   1\n",
			Warn, "Ubuntu",
		},
		{
			"shellforge at 2, another at 1",
			"  NAME                  STATE     VERSION\n  Ubuntu                Stopped   1\n* shellforge-sandbox    Running   2\n",
			OK, "",
		},
		{
			"the shellforge sandbox distribution itself is at version 1",
			"  NAME                  STATE     VERSION\n* shellforge-sandbox    Running   1\n",
			Fail, "",
		},
		{
			"header only, no distributions",
			"  NAME      STATE     VERSION\n",
			Warn, "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyWSLList(tt.text)
			if got.Level != tt.wantLevel {
				t.Errorf("classifyWSLList(%q).Level = %v, want %v", tt.text, got.Level, tt.wantLevel)
			}
			if got.OtherVersion1Name != tt.wantOther {
				t.Errorf("classifyWSLList(%q).OtherVersion1Name = %q, want %q", tt.text, got.OtherVersion1Name, tt.wantOther)
			}
		})
	}
}

func TestClassifyWSLVersion(t *testing.T) {
	tests := []struct {
		name string
		text string
		want Level
	}{
		{"current kernel", "WSL version: 2.0.9.0\nKernel version: 5.15.146.1\n", OK},
		{"newer kernel", "Kernel version: 6.6.30.0\n", OK},
		{"old kernel", "Kernel version: 4.19.128\n", Fail},
		{"unparseable", "something unexpected\n", Warn},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyWSLVersion(tt.text); got != tt.want {
				t.Errorf("classifyWSLVersion(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}

// TestWSLInstalledIsClassifiedByExitCodeNotByProse is the extra test the
// ticket's own test plan names: classification never touches the localized
// prose wsl --status prints.
func TestWSLInstalledIsClassifiedByExitCodeNotByProse(t *testing.T) {
	tests := []struct {
		name   string
		stdout string
		code   int
		want   Level
	}{
		{"german, exit 0", "Der Standarddistributionstyp ist WSL2.\n", 0, OK},
		{"french, exit 0", "Le type de distribution par defaut est WSL 2.\n", 0, OK},
		{"english, exit 0", "Default Distribution Version: 2\n", 0, OK},
		{"english not installed, exit 1", "WSL is not installed.\n", 1, Fail},
		{"exit 0 with empty stdout", "", 0, OK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := wslInstalledProbe{
				baseProbe: baseProbe{id: "wsl_installed", docAnchor: docAnchorWSLNotInstalled, windowsOnly: true},
				r:         newFakeRunner(fakeResponse{stdout: []byte(tt.stdout), code: tt.code}),
			}
			res := p.Run(context.Background())
			if res.Status != tt.want {
				t.Errorf("wsl --status exit %d = %v, want %v", tt.code, res.Status, tt.want)
			}
		})
	}
}

func TestWSLVersion2ProbeIsFullyPopulated(t *testing.T) {
	p := wslVersion2Probe{
		baseProbe: baseProbe{id: "wsl_version_2", docAnchor: docAnchorWSLVersion1, windowsOnly: true},
		r:         newFakeRunner(fakeResponse{stdout: utf16leBytes("  NAME      STATE     VERSION\n* Ubuntu     Running   2\n", true)}),
	}
	res := p.Run(context.Background())
	if res.Status != OK {
		t.Errorf("Status = %v, want OK", res.Status)
	}
	if res.Detail == "" || res.Remediation == "" || res.DocAnchor == "" {
		t.Errorf("wsl_version_2 result has an empty required field: %+v", res)
	}
}

func TestWSLKernelProbeIsFullyPopulated(t *testing.T) {
	p := wslKernelProbe{
		baseProbe: baseProbe{id: "wsl_kernel_current", docAnchor: docAnchorWSLKernelOutdated, windowsOnly: true},
		r:         newFakeRunner(fakeResponse{stdout: utf16leBytes("Kernel version: 5.15.146.1\n", true)}),
	}
	res := p.Run(context.Background())
	if res.Status != OK {
		t.Errorf("Status = %v, want OK", res.Status)
	}
	if res.Detail == "" || res.Remediation == "" || res.DocAnchor == "" {
		t.Errorf("wsl_kernel_current result has an empty required field: %+v", res)
	}
}
