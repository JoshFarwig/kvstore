package main

import (
	"errors"
	"fmt"
	"strconv"
)

type Config struct {
	NodeID          string
	CPUThresholdPct float64
	MemThresholdPct float64
	Host            string
	Port            string
}

func loadConfig(getenv func(string) string) (Config, error) {
	nodeID := getenv("NODE_ID")
	if nodeID == "" {
		return Config{}, errors.New("NODE_ID not set")
	}

	cpu, err := parseOptionalPct(getenv, "CPU_PCT_CAP")
	if err != nil {
		return Config{}, err
	}
	mem, err := parseOptionalPct(getenv, "MEM_PCT_CAP")
	if err != nil {
		return Config{}, err
	}

	host := getenv("HOST")
	port := getenv("PORT")
	if port == "" {
		port = "8080"
	}

	return Config{nodeID, cpu, mem, host, port}, nil
}

func parseOptionalPct(getenv func(string) string, env string) (float64, error) {
	v := getenv(env)
	if v == "" {
		return -1, nil
	}
	pct, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", env, err)
	}
	return pct, nil
}
