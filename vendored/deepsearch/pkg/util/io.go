package util

import "os"

func Exists(name string) bool {
	if _, e := os.Stat(name); e != nil {
		if os.IsNotExist(e) {
			return false
		}
	}

	return true
}
