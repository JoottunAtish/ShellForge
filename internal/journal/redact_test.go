package journal

import "testing"

// TestRedactHandlesEveryClosedListPattern is a table over the closed list
// documented on Redact and in doc.go: at least two cases per rule, each
// asserting the exact output string and true.
func TestRedactHandlesEveryClosedListPattern(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		// Rule 1: KEY=VALUE, KEY a whole shell word from the closed list.
		{
			name: "rule1/export password",
			raw:  "export PASSWORD=hunter2 && ./deploy.sh",
			want: "export PASSWORD=[redacted] && ./deploy.sh",
		},
		{
			name: "rule1/db token env var",
			raw:  "DB_TOKEN=abc123 make run",
			want: "DB_TOKEN=[redacted] make run",
		},

		// Rule 2: --password, --token, --api-key, --secret, --auth,
		// --credential, both the space and the = forms.
		{
			name: "rule2/space form",
			raw:  "curl --password hunter2 https://example.com",
			want: "curl --password [redacted] https://example.com",
		},
		{
			name: "rule2/equals form",
			raw:  "curl --api-key=abc123 https://example.com",
			want: "curl --api-key=[redacted] https://example.com",
		},

		// Rule 3: -p after mysql/psql/mysqldump, attached or spaced; curl -u
		// keeps the user and redacts only the password.
		{
			name: "rule3/mysql attached -p",
			raw:  "mysqldump -pSuperSecret db > backup.sql",
			want: "mysqldump -p [redacted] db > backup.sql",
		},
		{
			name: "rule3/psql spaced -p",
			raw:  "psql -p hunter2 -U admin db",
			want: "psql -p [redacted] -U admin db",
		},
		{
			name: "rule3/curl -u keeps user",
			raw:  "curl -u admin:hunter2 https://example.com",
			want: "curl -u admin:[redacted] https://example.com",
		},
		{
			name: "rule3/mysql -p with an intervening -u flag",
			raw:  "mysql -u root -phunter2 shipping",
			want: "mysql -u root -p [redacted] shipping",
		},
		{
			name: "rule1/pgpassword has no separator before the word password",
			raw:  "PGPASSWORD=hunter2 psql -h db.internal -U atlas",
			want: "PGPASSWORD=[redacted] psql -h db.internal -U atlas",
		},
		{
			name: "rule1/mysql_pwd",
			raw:  "MYSQL_PWD=hunter2 mysql -u root",
			want: "MYSQL_PWD=[redacted] mysql -u root",
		},
		{
			name: "rule1/ssh passphrase",
			raw:  "SSH_PASSPHRASE=hunter2 ssh-add ~/.ssh/id_ed25519",
			want: "SSH_PASSPHRASE=[redacted] ssh-add ~/.ssh/id_ed25519",
		},

		// Rule 4: Authorization: Bearer|Basic, case insensitive on the
		// header name and the scheme.
		{
			name: "rule4/bearer",
			raw:  "curl -H 'Authorization: Bearer abc123def456' https://example.com",
			want: "curl -H 'Authorization: Bearer [redacted]' https://example.com",
		},
		{
			name: "rule4/basic lowercase header",
			raw:  "curl -H 'authorization: basic dXNlcjpwYXNz' https://example.com",
			want: "curl -H 'Authorization: Basic [redacted]' https://example.com",
		},

		// Rule 5: anything between -----BEGIN and -----END, inclusive.
		{
			name: "rule5/rsa private key",
			raw:  "cat id_rsa: -----BEGIN RSA PRIVATE KEY-----\nMIIBOgIBAAJB\n-----END RSA PRIVATE KEY-----",
			want: "cat id_rsa: [redacted key]",
		},
		{
			name: "rule5/openssh private key",
			raw:  "-----BEGIN OPENSSH PRIVATE KEY-----\nZZZ123\n-----END OPENSSH PRIVATE KEY-----",
			want: "[redacted key]",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, changed := Redact(tc.raw)
			if got != tc.want {
				t.Errorf("Redact(%q) = %q, want %q", tc.raw, got, tc.want)
			}
			if !changed {
				t.Errorf("Redact(%q) reported changed = false, want true", tc.raw)
			}
		})
	}
}

// TestRedactLeavesABareSha256SumAlone pins the reason Redact is a closed
// list rather than an entropy or length heuristic: files-04 asks the
// learner to compare sha256 sums, so a bare 64 character hex string must
// survive untouched.
func TestRedactLeavesABareSha256SumAlone(t *testing.T) {
	sum := "a94a8fe5ccb19ba61c4c0873d391e987982fbbd3e94a8fe5ccb19ba61c4c0871"
	if len(sum) != 64 {
		t.Fatalf("test fixture is %d characters, want 64", len(sum))
	}
	raw := "sha256sum quest.txt | grep " + sum

	got, changed := Redact(raw)
	if got != raw {
		t.Errorf("Redact(%q) = %q, want it byte identical", raw, got)
	}
	if changed {
		t.Errorf("Redact(%q) reported changed = true, want false", raw)
	}
}

// TestRedactLeavesAnOrdinaryCommandByteIdentical is the baseline: nothing on
// the closed list should ever touch a command that names none of it.
func TestRedactLeavesAnOrdinaryCommandByteIdentical(t *testing.T) {
	raw := "ls -la /home/learner/quest"
	got, changed := Redact(raw)
	if got != raw {
		t.Errorf("Redact(%q) = %q, want it byte identical", raw, got)
	}
	if changed {
		t.Errorf("Redact(%q) reported changed = true, want false", raw)
	}
}

// TestRedactDoesNotCatchAWordMerelyContainingPass pins the word-boundary
// rule: PASSPORT_ID must not be caught by the "pass" spelling.
func TestRedactDoesNotCatchAWordMerelyContainingPass(t *testing.T) {
	raw := "PASSPORT_ID=abc123 ./enroll.sh"
	got, changed := Redact(raw)
	if got != raw {
		t.Errorf("Redact(%q) = %q, want it byte identical", raw, got)
	}
	if changed {
		t.Errorf("Redact(%q) reported changed = true, want false", raw)
	}
}

// TestRedactLeavesDockerRunUidGidAlone pins the fix for reCurlUserPass
// matching any -u user:pass shape rather than only curl's: docker run's
// -u uid:gid must survive untouched because the command is not curl.
func TestRedactLeavesDockerRunUidGidAlone(t *testing.T) {
	raw := "docker run -u 1000:1000 --rm -it alpine sh"
	got, changed := Redact(raw)
	if got != raw {
		t.Errorf("Redact(%q) = %q, want it byte identical", raw, got)
	}
	if changed {
		t.Errorf("Redact(%q) reported changed = true, want false", raw)
	}
}
