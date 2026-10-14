// Command mw：中间件 server（正文 §8 curl 实测行引用）。
//
// 运行: go run ./exercises/p30/cmd/mw
// 验证:
//
//	curl -s localhost:8080/greet                      → bad auth param
//	curl -s -H "auth: 123456" localhost:8080/greet   → Welcome!
package main

import (
	"fmt"
	"log"
	"net/http"
)

func greetings(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintln(w, "Welcome!")
}

func logHandler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Println("req:", r.URL.Path)
		h.ServeHTTP(w, r)
	})
}

func authHandler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("auth") != "123456" {
			http.Error(w, "bad auth param", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func main() {
	http.Handle("/greet", logHandler(authHandler(http.HandlerFunc(greetings))))
	log.Fatal(http.ListenAndServe("localhost:8080", nil))
}
