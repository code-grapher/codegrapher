// Command app is a tiny HTTP server over the store.
package main

import (
	"fmt"
	"net/http"

	"example.com/go-small/internal/store"
)

func main() {
	cache := store.NewCache()
	cache.Warm(map[string]string{"greeting": "hello"})

	mux := http.NewServeMux()
	mux.HandleFunc("/greet", handleGreet(cache))
	mux.HandleFunc("/health", handleHealth)

	_ = http.ListenAndServe(":8080", mux)
}

func handleGreet(c *store.Cache) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v, err := c.Lookup("greeting")
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		fmt.Fprintln(w, v)
	}
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}
