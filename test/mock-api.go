//go:build ignore
// +build ignore

// mock-api is a standalone mock Hookbase API server for e2e testing.
// Build: go build -o bin/mock-api test/mock-api.go
// Run: ./bin/mock-api
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
)

var (
	mu      sync.Mutex
	nextID  atomic.Int64
	sources = map[string]map[string]interface{}{}
	dests   = map[string]map[string]interface{}{}
	routes  = map[string]map[string]interface{}{}
	tunnels = map[string]map[string]interface{}{}
	apikeys = map[string]map[string]interface{}{}
)

func genID(prefix string) string {
	return fmt.Sprintf("%s_%d", prefix, nextID.Add(1))
}

func main() {
	mux := http.NewServeMux()

	// Generic CRUD handler factory
	registerCRUD := func(path, prefix, singular string, store *map[string]map[string]interface{}) {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()

			p := strings.TrimSuffix(r.URL.Path, "/")
			if p == path {
				// Collection
				switch r.Method {
				case "POST":
					var body map[string]interface{}
					json.NewDecoder(r.Body).Decode(&body)
					id := genID(prefix)
					body["id"] = id
					(*store)[id] = body
					w.WriteHeader(201)
					json.NewEncoder(w).Encode(map[string]interface{}{singular: body})
				case "GET":
					var items []map[string]interface{}
					for _, v := range *store {
						items = append(items, v)
					}
					json.NewEncoder(w).Encode(map[string]interface{}{singular + "s": items, "total": len(items)})
				}
				return
			}

			// Individual: /api/xxx/{id}
			id := p[len(path)+1:]
			item, ok := (*store)[id]
			switch r.Method {
			case "GET":
				if !ok {
					w.WriteHeader(404)
					json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
					return
				}
				json.NewEncoder(w).Encode(map[string]interface{}{singular: item})
			case "PATCH", "PUT":
				if !ok {
					w.WriteHeader(404)
					json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
					return
				}
				var body map[string]interface{}
				json.NewDecoder(r.Body).Decode(&body)
				for k, v := range body {
					item[k] = v
				}
				json.NewEncoder(w).Encode(map[string]interface{}{singular: item})
			case "DELETE":
				if !ok {
					w.WriteHeader(404)
					json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
					return
				}
				delete(*store, id)
				w.WriteHeader(204)
			}
		})
		// Also register the prefix path for individual resources
		mux.HandleFunc(path+"/", func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()

			id := r.URL.Path[len(path)+1:]
			item, ok := (*store)[id]
			switch r.Method {
			case "GET":
				if !ok {
					w.WriteHeader(404)
					json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
					return
				}
				json.NewEncoder(w).Encode(map[string]interface{}{singular: item})
			case "PATCH", "PUT":
				if !ok {
					w.WriteHeader(404)
					json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
					return
				}
				var body map[string]interface{}
				json.NewDecoder(r.Body).Decode(&body)
				for k, v := range body {
					item[k] = v
				}
				json.NewEncoder(w).Encode(map[string]interface{}{singular: item})
			case "DELETE":
				if !ok {
					w.WriteHeader(404)
					json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
					return
				}
				delete(*store, id)
				w.WriteHeader(204)
			}
		})
	}

	registerCRUD("/api/sources", "src", "source", &sources)
	registerCRUD("/api/destinations", "dst", "destination", &dests)
	registerCRUD("/api/routes", "rt", "route", &routes)
	// Tunnels: special handling to return authToken on create
	mux.HandleFunc("/api/tunnels", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method == "POST" {
			var body map[string]interface{}
			json.NewDecoder(r.Body).Decode(&body)
			id := genID("tun")
			body["id"] = id
			body["authToken"] = "tok_" + id
			tunnels[id] = body
			w.WriteHeader(201)
			json.NewEncoder(w).Encode(map[string]interface{}{"tunnel": body})
			return
		}
		// List
		var items []map[string]interface{}
		for _, v := range tunnels {
			items = append(items, v)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"tunnels": items, "total": len(items)})
	})
	mux.HandleFunc("/api/tunnels/", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		id := r.URL.Path[len("/api/tunnels/"):]
		item, ok := tunnels[id]
		switch r.Method {
		case "GET":
			if !ok {
				w.WriteHeader(404)
				json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
				return
			}
			json.NewEncoder(w).Encode(map[string]interface{}{"tunnel": item})
		case "DELETE":
			if !ok {
				w.WriteHeader(404)
				return
			}
			delete(tunnels, id)
			w.WriteHeader(204)
		}
	})

	// API keys: special response envelope
	mux.HandleFunc("/api/api-keys", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method == "POST" {
			var body map[string]interface{}
			json.NewDecoder(r.Body).Decode(&body)
			id := genID("key")
			body["id"] = id
			body["keyPrefix"] = "whr_test"
			body["key"] = "whr_test_" + id
			apikeys[id] = body
			w.WriteHeader(201)
			json.NewEncoder(w).Encode(map[string]interface{}{"data": body})
			return
		}
		// List
		var items []map[string]interface{}
		for _, v := range apikeys {
			items = append(items, v)
		}
		json.NewEncoder(w).Encode(items)
	})
	mux.HandleFunc("/api/api-keys/", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		id := r.URL.Path[len("/api/api-keys/"):]
		item, ok := apikeys[id]
		switch r.Method {
		case "GET":
			if !ok {
				w.WriteHeader(404)
				json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
				return
			}
			json.NewEncoder(w).Encode(map[string]interface{}{"data": item})
		case "DELETE":
			if !ok {
				w.WriteHeader(404)
				return
			}
			delete(apikeys, id)
			w.WriteHeader(204)
		}
	})

	// Health check
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	log.Println("Mock Hookbase API listening on :8787")
	log.Fatal(http.ListenAndServe(":8787", mux))
}
