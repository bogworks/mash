//go:build !unix

package config

func checkConfigMode(string) error { return nil }
