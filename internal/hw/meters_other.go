//go:build !(linux && (amd64 || arm64))

package hw

// Meters reads the mixer's meter segment (Linux on amd64 and arm64 only).
type Meters struct {
	Key int
}

// Read reports ErrNoMeters here.
func (m *Meters) Read(r *MeterReading) error { return ErrNoMeters }

// Close does nothing here.
func (m *Meters) Close() error { return nil }
