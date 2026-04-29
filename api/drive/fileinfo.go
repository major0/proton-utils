package drive

// FileInfo provides POSIX stat()-like metadata for a Link.
// Name is lazy — it calls Link.Name() which decrypts on demand.
// Each call to Name() performs a full decryption; callers that need
// the name more than once should cache the result locally.
type FileInfo struct {
	LinkID     string
	Name       func() (string, error)
	Size       int64
	ModifyTime int64
	CreateTime int64
	MIMEType   string
	IsDir      bool
	BlockSizes []int64
}
