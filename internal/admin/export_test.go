package admin

// SetAfterMetaGet installs a hook that runs between SetMeta's read and its write; it returns the previous one.
func SetAfterMetaGet(f func()) (prev func()) { prev, afterMetaGet = afterMetaGet, f; return }
