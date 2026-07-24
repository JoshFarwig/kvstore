package main

import (
	"github.com/JoshFarwig/kvstore/server"
	"github.com/JoshFarwig/kvstore/store"
)

func main() {
	store := store.NewStore()

	server.NewServer(store)
}
