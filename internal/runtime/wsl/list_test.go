package wsl

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
	"unicode/utf16"
)

// encodeUTF16LE builds the exact byte shape wsl.exe writes to its own
// stdout: an optional 0xFF 0xFE BOM followed by two bytes per UTF-16 code
// unit, little endian. Building fixtures this way, rather than typing raw
// byte literals, is what makes the fixture "real" in the sense the ticket
// asks for: it goes through the same encoding wsl.exe uses, surrogate pairs
// included.
func encodeUTF16LE(withBOM bool, s string) []byte {
	var buf bytes.Buffer
	if withBOM {
		buf.Write([]byte{0xFF, 0xFE})
	}
	for _, unit := range utf16.Encode([]rune(s)) {
		var b [2]byte
		binary.LittleEndian.PutUint16(b[:], unit)
		buf.Write(b[:])
	}
	return buf.Bytes()
}

func TestParseListDecodesRealUTF16LEWithABOM(t *testing.T) {
	text := "  NAME                   STATE           VERSION\r\n" +
		"  Ubuntu                 Running         2\r\n" +
		"  Debian                 Stopped         2\r\n" +
		"  shellforge-sandbox     Running         2\r\n"
	got, err := parseList(encodeUTF16LE(true, text))
	if err != nil {
		t.Fatalf("parseList: %v", err)
	}
	want := []distro{
		{Name: "Ubuntu", State: "Running", Version: 2},
		{Name: "Debian", State: "Stopped", Version: 2},
		{Name: "shellforge-sandbox", State: "Running", Version: 2},
	}
	if len(got) != len(want) {
		t.Fatalf("parseList returned %d row(s), want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, got[i], want[i])
		}
		if strings.ContainsRune(got[i].Name, 0) || strings.ContainsRune(got[i].State, 0) {
			t.Errorf("row %d contains a NUL byte: %+v", i, got[i])
		}
	}
}

func TestParseListMarksTheDefaultDistribution(t *testing.T) {
	text := "  NAME                   STATE           VERSION\r\n" +
		"* Ubuntu                 Running         2\r\n" +
		"  Debian                 Stopped         2\r\n"
	got, err := parseList(encodeUTF16LE(true, text))
	if err != nil {
		t.Fatalf("parseList: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("parseList returned %d row(s), want 2: %+v", len(got), got)
	}
	if !got[0].Default {
		t.Errorf("row 0 (Ubuntu, the starred row) Default = false, want true")
	}
	if got[0].Name != "Ubuntu" {
		t.Errorf("row 0 Name = %q, want %q (the asterisk must not be part of it)", got[0].Name, "Ubuntu")
	}
	if got[1].Default {
		t.Errorf("row 1 (Debian, not starred) Default = true, want false")
	}
}

func TestParseListAcceptsANameContainingASpace(t *testing.T) {
	text := "  NAME                   STATE           VERSION\r\n" +
		"  Ubuntu 22.04           Stopped         2\r\n"
	got, err := parseList(encodeUTF16LE(true, text))
	if err != nil {
		t.Fatalf("parseList: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("parseList returned %d row(s), want 1: %+v", len(got), got)
	}
	want := distro{Name: "Ubuntu 22.04", State: "Stopped", Version: 2}
	if got[0] != want {
		t.Errorf("row 0 = %+v, want %+v", got[0], want)
	}
}

func TestParseListReturnsAnEmptyListForNoDistributions(t *testing.T) {
	t.Run("BOM plus header only", func(t *testing.T) {
		text := "  NAME                   STATE           VERSION\r\n"
		got, err := parseList(encodeUTF16LE(true, text))
		if err != nil {
			t.Fatalf("parseList: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("parseList returned %d row(s), want 0: %+v", len(got), got)
		}
	})

	t.Run("zero bytes", func(t *testing.T) {
		got, err := parseList(nil)
		if err != nil {
			t.Fatalf("parseList: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("parseList returned %d row(s), want 0: %+v", len(got), got)
		}
	})
}

func TestParseListReportsAWSL1Row(t *testing.T) {
	text := "  NAME                   STATE           VERSION\r\n" +
		"  legacy-distro           Stopped         1\r\n"
	got, err := parseList(encodeUTF16LE(true, text))
	if err != nil {
		t.Fatalf("parseList: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("parseList returned %d row(s), want 1: %+v", len(got), got)
	}
	if got[0].Version != 1 {
		t.Errorf("Version = %d, want 1 (Provision refuses a WSL1 row itself, parseList must not)", got[0].Version)
	}
}

func TestParseListRefusesTruncatedInput(t *testing.T) {
	t.Run("cut mid code unit", func(t *testing.T) {
		text := "  NAME                   STATE           VERSION\r\n" +
			"  Ubuntu                 Running         2\r\n"
		full := encodeUTF16LE(true, text)
		truncated := full[:len(full)-1]
		got, err := parseList(truncated)
		if err == nil {
			t.Fatal("parseList on input truncated mid code unit = nil error, want a refusal")
		}
		if got != nil {
			t.Errorf("parseList on truncated input returned %+v, want a nil slice", got)
		}
	})

	t.Run("last row has only one field", func(t *testing.T) {
		text := "  NAME                   STATE           VERSION\r\n" +
			"  Ubuntu                 Running         2\r\n" +
			"  OnlyOneField\r\n"
		got, err := parseList(encodeUTF16LE(true, text))
		if err == nil {
			t.Fatal("parseList with a one-field last row = nil error, want a refusal")
		}
		if !strings.Contains(err.Error(), "OnlyOneField") {
			t.Errorf("parseList error = %q, want it to name the offending row", err.Error())
		}
		if got != nil {
			t.Errorf("parseList with a malformed row returned %+v, want a nil slice", got)
		}
	})
}

func TestParseListRefusesAMalformedRow(t *testing.T) {
	t.Run("non-integer version", func(t *testing.T) {
		text := "  NAME                   STATE           VERSION\r\n" +
			"  Ubuntu                 Running         two\r\n"
		got, err := parseList(encodeUTF16LE(true, text))
		if err == nil {
			t.Fatal("parseList with a non-integer version = nil error, want a refusal")
		}
		if got != nil {
			t.Errorf("parseList with a non-integer version returned %+v, want a nil slice", got)
		}
	})

	t.Run("fewer than three fields", func(t *testing.T) {
		text := "  NAME                   STATE           VERSION\r\n" +
			"  Running 2\r\n"
		got, err := parseList(encodeUTF16LE(true, text))
		if err == nil {
			t.Fatal("parseList with a two-field row = nil error, want a refusal")
		}
		if got != nil {
			t.Errorf("parseList with a two-field row returned %+v, want a nil slice", got)
		}
	})
}

func TestParseListRefusesAnOddLengthBody(t *testing.T) {
	// One BOM (2 bytes, even) followed by an odd number of body bytes.
	body := []byte{0xFF, 0xFE, 0x41, 0x00, 0x42}
	got, err := parseList(body)
	if err == nil {
		t.Fatal("parseList on an odd-length body = nil error, want a refusal")
	}
	if !strings.Contains(err.Error(), "odd") {
		t.Errorf("parseList error = %q, want it to mention the odd byte count", err.Error())
	}
	if got != nil {
		t.Errorf("parseList on an odd-length body returned %+v, want a nil slice", got)
	}
}

func TestDecodeUTF16LERefusesABigEndianBOM(t *testing.T) {
	body := []byte{0xFE, 0xFF, 0x00, 0x41}
	_, err := decodeUTF16LE(body)
	if err == nil {
		t.Fatal("decodeUTF16LE with a big-endian BOM = nil error, want a refusal")
	}
}

func TestDecodeUTF16LEDecodesASurrogatePair(t *testing.T) {
	// U+1F600, GRINNING FACE, is outside the BMP and requires a surrogate
	// pair in UTF-16.
	want := "\U0001F600"
	got, err := decodeUTF16LE(encodeUTF16LE(true, want))
	if err != nil {
		t.Fatalf("decodeUTF16LE: %v", err)
	}
	if got != want {
		t.Errorf("decodeUTF16LE round trip = %q, want %q", got, want)
	}
}

func TestParseQuietListDecodesNames(t *testing.T) {
	text := "Ubuntu\r\nDebian\r\nshellforge-sandbox\r\n"
	got, err := parseQuietList(encodeUTF16LE(true, text))
	if err != nil {
		t.Fatalf("parseQuietList: %v", err)
	}
	want := []string{"Ubuntu", "Debian", "shellforge-sandbox"}
	if len(got) != len(want) {
		t.Fatalf("parseQuietList = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("name %d = %q, want %q", i, got[i], want[i])
		}
	}
}
