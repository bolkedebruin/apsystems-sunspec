package source

import "testing"

func TestParseEventBits(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want [4]uint32
	}{
		{
			"all zero", "00000000000000000000000000000000",
			[4]uint32{0, 0, 0, 0},
		},
		{
			"bit 0", "1",
			[4]uint32{0x00000001, 0, 0, 0},
		},
		{
			"bit 31",
			// 32-char string, last char '1'
			"00000000000000000000000000000001",
			[4]uint32{0x80000000, 0, 0, 0},
		},
		{
			"bit 32 (first of slot 1)",
			"000000000000000000000000000000001",
			[4]uint32{0, 0x00000001, 0, 0},
		},
		{
			"observed sample — bit 40 set",
			// position 40 = '1', everything else '0'
			"00000000000000000000000000000000000000001000000000000000000000000000000000000000000000",
			[4]uint32{0, 0x00000100, 0, 0},
		},
		{
			"empty string",
			"",
			[4]uint32{0, 0, 0, 0},
		},
		{
			"too long is truncated at 128",
			// 130 chars, all '1'
			"1111111111111111111111111111111111111111111111111111111111111111" +
				"1111111111111111111111111111111111111111111111111111111111111111" +
				"11",
			[4]uint32{0xFFFFFFFF, 0xFFFFFFFF, 0xFFFFFFFF, 0xFFFFFFFF},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseEventBits(c.in)
			if got != c.want {
				t.Errorf("got %v want %v", got, c.want)
			}
		})
	}
}
