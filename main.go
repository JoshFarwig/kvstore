package main

import (
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"

	"github.com/JoshFarwig/kvstore/server"
	"github.com/JoshFarwig/kvstore/store"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

	s := store.NewStore()

	fmt.Println("Ἀεὶ ὁ θεὸς ὁ μέγας γεωμετρεῖ τὸ σύμπαν...")
	log.Fatal(http.ListenAndServe(":8080", server.NewServer(s)))
}
