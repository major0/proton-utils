package drive

import "github.com/ProtonMail/go-proton-api"

// Volume represents a Proton Drive volume.
// API-calling methods (ListShareMetadata, GetShareMetadata, GetShare)
// live on Client.
type Volume struct {
	ProtonVolume proton.Volume
}

// VolumeID returns the stable device identifier for this volume.
func (v *Volume) VolumeID() string { return v.ProtonVolume.VolumeID }
