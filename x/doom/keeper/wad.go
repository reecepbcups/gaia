package keeper

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"

	"cosmossdk.io/log"
)

// WADEnv names the environment variable that points at the IWAD.
const WADEnv = "GAIA_DOOM_WAD"

// DefaultWADName is the file the node looks for in its home directory when
// WADEnv is unset.
const DefaultWADName = "doom.wad"

// LoadWAD reads the IWAD this node should play, or returns nil if there is
// none. A nil WAD is fine on a chain whose params leave wad_hash empty; on a
// chain that is running DOOM the EndBlocker will refuse to make progress, which
// is the loud failure you want rather than a silent fork.
func LoadWAD(homePath string, logger log.Logger) []byte {
	path := os.Getenv(WADEnv)
	if path == "" {
		path = filepath.Join(homePath, DefaultWADName)
	}

	wad, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			logger.Error("failed to read doom wad", "path", path, "err", err)
		}
		return nil
	}

	sum := sha256.Sum256(wad)
	logger.Info("loaded doom wad", "path", path, "bytes", len(wad), "sha256", hex.EncodeToString(sum[:]))

	return wad
}
