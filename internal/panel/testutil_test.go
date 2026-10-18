package panel

import "os"

func writePrivate(path string, b []byte) error { return os.WriteFile(path, b, 0600) }
