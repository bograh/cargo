package apps

import "fmt"

// maxPort is the highest TCP port an app can serve on. The check used to be
// `< 1` only, while the error message already promised a range — so a value
// like 70000 was accepted and rendered straight into a Traefik service label.
const maxPort = 65535

// validatePort checks the port an app serves on against the range its own
// error message advertises.
func validatePort(p int32) error {
	if p < 1 || p > maxPort {
		return fmt.Errorf("%w: exposed_port must be 1-%d", ErrValidation, maxPort)
	}
	return nil
}
