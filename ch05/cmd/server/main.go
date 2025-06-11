package main

import (
	"log"

	"github.com/ishisaka/go_distribute/proglog/internal/server"
)

func main() {
	srv := server.NewHTTPServer(":8080")
	log.Fatal(srv.ListenAndServe())
}
