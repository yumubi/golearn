package p33gen

import "testing"

// 泛型二分（对照 C:go_frame/data_structure/binary_search.go）
func BinarySearch[T ~int | ~int64 | ~float64 | ~string](xs []T, target T) int {
	begin, end := 0, len(xs)-1
	for begin <= end {
		mid := begin + (end-begin)/2
		switch {
		case xs[mid] == target:
			return mid
		case xs[mid] < target:
			begin = mid + 1
		default:
			end = mid - 1
		}
	}
	return -1
}

func TestBinarySearch(t *testing.T) {
	xs := []int{1, 3, 5, 7, 9}
	if got := BinarySearch(xs, 7); got != 3 {
		t.Fatal(got)
	}
	if got := BinarySearch(xs, 4); got != -1 {
		t.Fatal(got)
	}
}
