package journal

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestReadTSV(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  []Entry
	}{
		{
			name:  "empty file",
			input: "",
			want:  nil,
		},
		{
			name:  "one well formed line",
			input: "1765432100.123456\t0\t/home/learner\techo hi\n",
			want: []Entry{
				{Seq: 1, TS: time.Unix(1765432100, 123456000).UTC(), Exit: 0, Cwd: "/home/learner", Raw: "echo hi"},
			},
		},
		{
			name:  "tab inside the command",
			input: "1765432100.000000\t0\t/home/learner\tprintf 'a\tb'\n",
			want: []Entry{
				{Seq: 1, TS: time.Unix(1765432100, 0).UTC(), Exit: 0, Cwd: "/home/learner", Raw: "printf 'a\tb'"},
			},
		},
		{
			name:  "truncated final line with two fields and no newline",
			input: "1765432100.000000\t0\t/home/learner\tls\n1765432101.000000\t1",
			want: []Entry{
				{Seq: 1, TS: time.Unix(1765432100, 0).UTC(), Exit: 0, Cwd: "/home/learner", Raw: "ls"},
			},
		},
		{
			name:  "complete looking final line with no trailing newline",
			input: "1765432100.000000\t0\t/home/learner\tls\n1765432101.000000\t1\t/home/learner\tpwd",
			want: []Entry{
				{Seq: 1, TS: time.Unix(1765432100, 0).UTC(), Exit: 0, Cwd: "/home/learner", Raw: "ls"},
			},
		},
		{
			name:  "malformed line in the middle",
			input: "1765432100.000000\t0\t/home/learner\tls\nbad\tline\n1765432102.000000\t0\t/home/learner\tpwd\n",
			want: []Entry{
				{Seq: 1, TS: time.Unix(1765432100, 0).UTC(), Exit: 0, Cwd: "/home/learner", Raw: "ls"},
				{Seq: 2, TS: time.Unix(1765432102, 0).UTC(), Exit: 0, Cwd: "/home/learner", Raw: "pwd"},
			},
		},
		{
			name:  "negative exit code",
			input: "1765432100.000000\t-1\t/home/learner\tfalse\n",
			want: []Entry{
				{Seq: 1, TS: time.Unix(1765432100, 0).UTC(), Exit: -1, Cwd: "/home/learner", Raw: "false"},
			},
		},
		{
			name:  "comma radix timestamp",
			input: "1765432100,123456\t0\t/home/learner\techo hi\n",
			want: []Entry{
				{Seq: 1, TS: time.Unix(1765432100, 123456000).UTC(), Exit: 0, Cwd: "/home/learner", Raw: "echo hi"},
			},
		},
		{
			name:  "short fraction",
			input: "1765432100.12\t0\t/home/learner\techo hi\n",
			want: []Entry{
				{Seq: 1, TS: time.Unix(1765432100, 120000000).UTC(), Exit: 0, Cwd: "/home/learner", Raw: "echo hi"},
			},
		},
		{
			name:  "long fraction",
			input: "1765432100.1234569\t0\t/home/learner\techo hi\n",
			want: []Entry{
				{Seq: 1, TS: time.Unix(1765432100, 123456000).UTC(), Exit: 0, Cwd: "/home/learner", Raw: "echo hi"},
			},
		},
		{
			name:  "no radix character",
			input: "1765432100\t0\t/home/learner\techo hi\n",
			want: []Entry{
				{Seq: 1, TS: time.Unix(1765432100, 0).UTC(), Exit: 0, Cwd: "/home/learner", Raw: "echo hi"},
			},
		},
		{
			name:  "unparsable timestamp",
			input: "not-a-time\t0\t/home/learner\techo hi\n",
			want:  nil,
		},
		{
			name:  "unparsable exit code",
			input: "1765432100.000000\tnot-a-code\t/home/learner\techo hi\n",
			want:  nil,
		},
		{
			name:  "CRLF line endings",
			input: "1765432100.000000\t0\t/home/learner\techo hi\r\n",
			want: []Entry{
				{Seq: 1, TS: time.Unix(1765432100, 0).UTC(), Exit: 0, Cwd: "/home/learner", Raw: "echo hi"},
			},
		},
		{
			name:  "blank line",
			input: "\n1765432100.000000\t0\t/home/learner\techo hi\n",
			want: []Entry{
				{Seq: 1, TS: time.Unix(1765432100, 0).UTC(), Exit: 0, Cwd: "/home/learner", Raw: "echo hi"},
			},
		},
		{
			name: "seq numbering",
			input: strings.Join([]string{
				"1765432100.000000\t0\t/home/learner\tone",
				"bad",
				"1765432101.000000\t0\t/home/learner\ttwo",
				"also\tbad",
				"1765432102.000000\t0\t/home/learner\tthree",
				"",
			}, "\n"),
			want: []Entry{
				{Seq: 1, TS: time.Unix(1765432100, 0).UTC(), Exit: 0, Cwd: "/home/learner", Raw: "one"},
				{Seq: 2, TS: time.Unix(1765432101, 0).UTC(), Exit: 0, Cwd: "/home/learner", Raw: "two"},
				{Seq: 3, TS: time.Unix(1765432102, 0).UTC(), Exit: 0, Cwd: "/home/learner", Raw: "three"},
			},
		},
		{
			name:  "level id is left empty",
			input: "1765432100.000000\t0\t/home/learner\techo hi\n",
			want: []Entry{
				{Seq: 1, TS: time.Unix(1765432100, 0).UTC(), Exit: 0, Cwd: "/home/learner", Raw: "echo hi", LevelID: "", UsedTab: false, UsedHistory: false},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ReadTSV(strings.NewReader(tc.input))
			if err != nil {
				t.Fatalf("ReadTSV: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %d entries, want %d: %+v", len(got), len(tc.want), got)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("entry %d = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestReadTSVPropagatesAReadError(t *testing.T) {
	sentinel := errors.New("boom")
	r := &failingReader{
		good: "1765432100.000000\t0\t/home/learner\techo hi\n",
		err:  sentinel,
	}

	got, err := ReadTSV(r)
	if err == nil {
		t.Fatal("ReadTSV returned a nil error")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("error does not wrap the sentinel: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("got %d entries before the error, want 1", len(got))
	}
}

// failingReader returns good exactly once, then err on every subsequent
// Read, so ReadTSV can be exercised against a stream that fails partway
// through rather than ending cleanly at io.EOF.
type failingReader struct {
	good string
	err  error
	done bool
}

func (r *failingReader) Read(p []byte) (int, error) {
	if !r.done {
		r.done = true
		n := copy(p, r.good)
		return n, nil
	}
	return 0, r.err
}

var _ io.Reader = (*failingReader)(nil)
