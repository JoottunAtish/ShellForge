package pty

import "testing"

func TestCSIScannerRecognizesAClear(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"clear(1) as terminfo writes it", "\x1b[H\x1b[2J\x1b[3J", true},
		{"erase display only", "\x1b[2J", true},
		{"erase scrollback only", "\x1b[3J", true},
		{"the DEC private spelling", "\x1b[?2J", true},
		{"erase to end of screen is not a clear", "\x1b[0J", false},
		{"erase to start of screen is not a clear", "\x1b[1J", false},
		{"a bare ED defaults to erase-to-end", "\x1b[J", false},
		{"home the cursor is not a clear", "\x1b[H", false},
		{"clearing a line is not clearing the screen", "\x1b[2K", false},
		{"a colour reset is not a clear", "\x1b[0m", false},
		{"plain text", "the quest folder is empty", false},
		{"an ESC that introduces nothing", "\x1b(B\x1b[2J", true},
		{"ESC ESC then a clear", "\x1b\x1b[2J", true},
		{"a clear buried in output", "before\x1b[2Jafter", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var c csiScanner
			if got := c.scan([]byte(tc.in)); got != tc.want {
				t.Errorf("scan(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestCSIScannerIgnoresTheAlternateScreen is the assertion the whole feature
// rests on. vim, less, man and htop erase constantly, and every one of them
// does it on the alternate screen buffer, which is also why quitting one
// restores what was underneath.
func TestCSIScannerIgnoresTheAlternateScreen(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"1049, the modern one", "\x1b[?1049h\x1b[2J\x1b[?1049l", false},
		{"1047, the older one", "\x1b[?1047h\x1b[2J\x1b[?1047l", false},
		{"47, the oldest one", "\x1b[?47h\x1b[2J\x1b[?47l", false},
		{"among other modes in one sequence", "\x1b[?1049;1h\x1b[2J", false},
		{"a clear after leaving counts", "\x1b[?1049h\x1b[2J\x1b[?1049l\x1b[2J", true},
		{"a clear before entering counts", "\x1b[2J\x1b[?1049h\x1b[2J", true},
		{"a reset of a mode never set leaves the primary screen", "\x1b[?1049l\x1b[2J", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var c csiScanner
			if got := c.scan([]byte(tc.in)); got != tc.want {
				t.Errorf("scan(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestCSIScannerSurvivesChunkBoundaries is why this is a state machine rather
// than a bytes.Contains. Escape sequences arrive split across Read calls at
// arbitrary offsets, and a scanner that assumed otherwise would miss roughly
// one clear in a hundred: often enough to reach a learner, rare enough to
// cost a day finding.
func TestCSIScannerSurvivesChunkBoundaries(t *testing.T) {
	const stream = "\x1b[?1049h\x1b[2J\x1b[?1049lready\x1b[H\x1b[2J\x1b[3J"

	for size := 1; size <= len(stream); size++ {
		var c csiScanner
		cleared := false
		for i := 0; i < len(stream); i += size {
			end := min(i+size, len(stream))
			if c.scan([]byte(stream[i:end])) {
				cleared = true
			}
		}
		if !cleared {
			t.Errorf("chunk size %d: the clear after the alternate screen was missed", size)
		}
	}
}

// TestCSIScannerAbandonsAnOversizedSequence pins the bound. The learner owns
// the shell writing to this terminal, so the parameter run can be any length
// they like; past the bound the sequence is abandoned rather than buffered,
// and the scanner must still be in a state that recognizes the next real
// clear.
func TestCSIScannerAbandonsAnOversizedSequence(t *testing.T) {
	var c csiScanner

	long := "\x1b["
	for i := 0; i < maxCSIParams*4; i++ {
		long += "1;"
	}
	long += "J"

	if c.scan([]byte(long)) {
		t.Error("an oversized parameter run was read as a clear")
	}
	if !c.scan([]byte("\x1b[2J")) {
		t.Error("the scanner did not recover: a real clear after an oversized sequence was missed")
	}
}
