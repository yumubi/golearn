package p33gen

import "testing"

type Set[T comparable] map[T]struct{}

func NewSet[T comparable](n int) Set[T] {
	m := make(map[T]struct{}, n)
	return Set[T](m)
}

func (s Set[T]) Add(e T)    { s[e] = struct{}{} }
func (s Set[T]) Remove(e T) { delete(s, e) }
func (s Set[T]) Exists(e T) bool {
	_, ok := s[e]
	return ok
}
func (s Set[T]) Len() int { return len(s) }

func TestSet(t *testing.T) {
	s := NewSet[string](4)
	s.Add("a")
	s.Add("b")
	if s.Len() != 2 || !s.Exists("a") {
		t.Fatal(s)
	}
	s.Remove("a")
	if s.Exists("a") {
		t.Fatal("remove failed")
	}
}
