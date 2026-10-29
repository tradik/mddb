package sliceutil

import (
	"reflect"
	"testing"
)

func TestUnique(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"empty", nil, []string{}},
		{"no dups", []string{"a", "b", "c"}, []string{"a", "b", "c"}},
		{"dups preserve first-seen order", []string{"b", "a", "b", "c", "a"}, []string{"b", "a", "c"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Unique(tc.in); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Unique(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestUniqueIntsDoesNotMutateInput(t *testing.T) {
	in := []int{1, 1, 2, 3, 3}
	_ = Unique(in)
	if !reflect.DeepEqual(in, []int{1, 1, 2, 3, 3}) {
		t.Errorf("Unique mutated its input: %v", in)
	}
	if got := Unique(in); !reflect.DeepEqual(got, []int{1, 2, 3}) {
		t.Errorf("Unique(ints) = %v, want [1 2 3]", got)
	}
}

func TestGroupTop(t *testing.T) {
	in := []string{"a1", "b1", "a2", "a3", "c1", "b2"}
	key := func(s string) string { return s[:1] }
	out, ranks := GroupTop(in, 2, key)
	if !reflect.DeepEqual(out, []string{"a1", "b1", "a2", "c1", "b2"}) || !reflect.DeepEqual(ranks, []int{1, 1, 2, 1, 2}) {
		t.Fatalf("perGroup 2: %v %v", out, ranks)
	}
	out, _ = GroupTop(in, 0, key)
	if !reflect.DeepEqual(out, []string{"a1", "b1", "c1"}) {
		t.Fatalf("perGroup 0 is 1: %v", out)
	}
	if out, _ := GroupTop([]string(nil), 3, key); len(out) != 0 {
		t.Fatal("nil input")
	}
}
