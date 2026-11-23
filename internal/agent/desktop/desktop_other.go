//go:build !windows

package desktop

func followInput() error { return nil }

func inputName() string { return "" }
