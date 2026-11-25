package keyfind

import (
	"reflect"
	"testing"
)

func TestParse(t *testing.T) {
	cases := []struct {
		in   string
		want Query
		ok   bool
	}{
		{"1429", Query{"", "1429"}, true},
		{"mob1429", Query{"mob", "1429"}, true},
		{"MOB-1429", Query{"mob", "1429"}, true},
		{"m14", Query{"m", "14"}, true},
		{"pl41", Query{"pl", "41"}, true},
		{"14 29", Query{"", "1429"}, true},
		{"  mob - 14 29 ", Query{"mob", "1429"}, true},
		{"mob", Query{}, false},
		{"", Query{}, false},
		{"14a2", Query{}, false},
	}
	for _, c := range cases {
		got, ok := Parse(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("Parse(%q) = %+v,%v want %+v,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		q     string
		key   string
		tier  string
		match []int
	}{
		{"1429", "MOB-1429", Exact, []int{4, 5, 6, 7}},
		{"mob1429", "MOB-1429", Exact, []int{0, 1, 2, 4, 5, 6, 7}},
		{"1429", "MOB-1428", Close, []int{4, 5, 6}},
		{"1429", "MOB-1421", Close, []int{4, 5, 6}},
		{"1429", "MOB-1629", Close, []int{4, 6, 7}},
		{"1429", "MOB-1249", Close, []int{4, 7}}, // swap: the two swapped digits stay plain
		{"m14", "MOB-1407", Prefix, []int{0, 4, 5}},
		{"14", "MOB-1411", Prefix, []int{4, 5}},
		{"pl41", "PLAT-412", Prefix, []int{0, 1, 5, 6}},
		{"pl41", "MOB-412", "", nil},
		{"zz14", "MOB-1407", "", nil},
		{"1429", "MOB-14290", Prefix, []int{4, 5, 6, 7}},
		{"1429", "MOB-142", "", nil},
		{"1429", "MOB-1327", "", nil},     // two substitutions
		{"1429", "MOB-4192", "", nil},     // not adjacent
		{"14", "MOB-15", Close, []int{4}}, // two digits: one off
		{"5", "MOB-6", "", nil},           // a single digit is never "close"
		{"1429", "NODASH", "", nil},
	}
	for _, c := range cases {
		q, ok := Parse(c.q)
		if !ok {
			t.Fatalf("Parse(%q) failed", c.q)
		}
		tier, match, ok := Classify(q, c.key)
		if (c.tier == "") == ok {
			t.Errorf("Classify(%q, %q) ok=%v, want tier %q", c.q, c.key, ok, c.tier)
			continue
		}
		if tier != c.tier || !reflect.DeepEqual(match, c.match) {
			t.Errorf("Classify(%q, %q) = %s %v, want %s %v", c.q, c.key, tier, match, c.tier, c.match)
		}
	}
}

func TestNumber(t *testing.T) {
	if Number("MOB-1429") != 1429 || Number("x") != 0 {
		t.Fatal("Number")
	}
}
