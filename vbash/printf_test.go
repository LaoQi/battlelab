package vbash

import "testing"

func TestPrintfFloatFormat(t *testing.T) {
	cases := []struct {
		format string
		values []string
		want   string
	}{
		{"%.3f", []string{"3.14159"}, "3.142"},
		{"%8.2f", []string{"2.5"}, "    2.50"},
		{"%-8.2f|", []string{"2.5"}, "2.50    |"},
		{"%08.3f", []string{"-2.5"}, "-002.500"},
		{"%e", []string{"1234.5"}, "1.234500e+03"},
		{"%.3e", []string{"1234.5"}, "1.234e+03"},
		{"%E", []string{"1234.5"}, "1.234500E+03"},
		{"%g", []string{"1000000"}, "1e+06"},
		{"%g", []string{"1234567"}, "1.23457e+06"},
		{"%g", []string{"0.0001"}, "0.0001"},
		{"%.0f", []string{"2.5"}, "2"},
		{"%+.1f", []string{"2.5"}, "+2.5"},
		{"%f", []string{"1"}, "1.000000"},
	}
	for _, c := range cases {
		got, warn := formatPrintf(c.format, c.values)
		if got != c.want {
			t.Fatalf("%s %v: got %q want %q", c.format, c.values, got, c.want)
		}
		if warn != "" {
			t.Fatalf("%s: 不应告警，got %q", c.format, warn)
		}
	}
}

func TestPrintfIntFormat(t *testing.T) {
	cases := []struct {
		format string
		values []string
		want   string
	}{
		{"%.3d", []string{"5"}, "005"},
		{"%.5d", []string{"-42"}, "-00042"},
		{"%.0d", []string{"7"}, "7"},
		{"%+d", []string{"5"}, "+5"},
		{"% d", []string{"5"}, " 5"},
		{"%#x", []string{"255"}, "0xff"},
		{"%#o", []string{"8"}, "010"},
		{"%08d", []string{"42"}, "00000042"},
		{"%u", []string{"-1"}, "18446744073709551615"},
		{"%x", []string{"-1"}, "ffffffffffffffff"},
		{"%d", []string{"010"}, "8"},
		{"%d", []string{"0x10"}, "16"},
	}
	for _, c := range cases {
		got, _ := formatPrintf(c.format, c.values)
		if got != c.want {
			t.Fatalf("%s %v: got %q want %q", c.format, c.values, got, c.want)
		}
	}
}

func TestPrintfInvalidNumber(t *testing.T) {
	cases := []struct {
		format string
		values []string
		want   string
		out    string
	}{
		{"%d", []string{"abc"}, "abc: invalid number", "0"},
		{"%d", []string{"3.9"}, "3.9: invalid number", "3"},
		{"%d", []string{"08"}, "08: invalid octal number", "0"},
		{"%d", []string{"99999999999999999999"}, "99999999999999999999: Numerical result out of range", "9223372036854775807"},
	}
	for _, c := range cases {
		got, warn := formatPrintf(c.format, c.values)
		if warn != c.want {
			t.Fatalf("%s %v: warn got %q want %q", c.format, c.values, warn, c.want)
		}
		if got != c.out {
			t.Fatalf("%s %v: out got %q want %q", c.format, c.values, got, c.out)
		}
	}
}

func TestPrintfStringPrecision(t *testing.T) {
	cases := []struct {
		format string
		values []string
		want   string
	}{
		{"%.2s", []string{"abcdef"}, "ab"},
		{"%5.2s|", []string{"abcdef"}, "   ab|"},
		{"%-5s|", []string{"ab"}, "ab   |"},
		{"%.0s", []string{"abc"}, ""},
	}
	for _, c := range cases {
		got, _ := formatPrintf(c.format, c.values)
		if got != c.want {
			t.Fatalf("%s %v: got %q want %q", c.format, c.values, got, c.want)
		}
	}
}
