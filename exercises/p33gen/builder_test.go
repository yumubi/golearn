package p33gen

import "testing"

// 局限三绕开：Getter/Setter 接口
type Entity interface {
	GetID() int64
	SetID(int64)
}

type User struct{ id int64 }

func (u *User) GetID() int64  { return u.id }
func (u *User) SetID(v int64) { u.id = v }

func Insert[E Entity](e E) (E, error) {
	if e.GetID() == 0 {
		e.SetID(1)
	}
	return e, nil
}

func TestGetterSetterConstraint(t *testing.T) {
	u, err := Insert(&User{})
	if err != nil || u.GetID() != 1 {
		t.Fatal(u, err)
	}
}

// 局限二实验：约束不能当类型用
type C interface{ ~int | ~string }

type MyID int

// var x C = 10 // 编译错：cannot use type C outside a type constraint

func Sum[T C](xs []T) int { return len(xs) }

func TestConstraintNotType(t *testing.T) {
	if Sum([]MyID{1, 2}) != 2 {
		t.Fatal("sum")
	}
}
