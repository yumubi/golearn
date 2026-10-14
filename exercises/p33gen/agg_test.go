package p33gen

import "testing"

type Column struct{ Name string }
type Aggregate struct{ Fn string }

type Selectable interface{ aggr() }

func (Column) aggr()    {}
func (Aggregate) aggr() {}

// 局限一：解开注释编译不过
// type Orm[T any] interface {
// 	Query[Q any](q Q) ([]T, error)
// }

func Select(cols ...Selectable) string {
	s := ""
	for i, c := range cols {
		if i > 0 {
			s += ", "
		}
		switch v := any(c).(type) {
		case Column:
			s += v.Name
		case Aggregate:
			s += v.Fn
		}
	}
	return s
}

func TestSelect(t *testing.T) {
	got := Select(Column{"name"}, Aggregate{"avg(age)"})
	want := "name, avg(age)"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
