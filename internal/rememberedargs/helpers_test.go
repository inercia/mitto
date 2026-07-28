package rememberedargs

import "os"

// readFileHelper is a tiny wrapper so tests do not import os directly.
func readFileHelper(path string) ([]byte, error) { return os.ReadFile(path) }
