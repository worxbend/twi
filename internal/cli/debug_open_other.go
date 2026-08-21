//go:build !unix

package cli

const (
	debugLogOpenUsesNoFollow = false
	debugLogOpenPlatformNote = "Non-Unix debug log opening does not provide Unix O_NOFOLLOW or exact owner-only ACL guarantees; it rejects unsafe paths it can observe before and after open."
)

// debugLogNoFollowFlags and debugLogReopenFlags add nothing: this platform has
// no portable no-follow open flag.
func debugLogNoFollowFlags() int { return 0 }

func debugLogReopenFlags() int { return 0 }

// checkDebugLogPathBeforeReopen inspects the path first, because without a
// no-follow flag that is the only chance to reject a symlink. It is weaker
// than the Unix guarantee -- the path could change between this check and the
// open -- which is why the opened descriptor is validated afterwards too.
func checkDebugLogPathBeforeReopen(path string) error {
	return validateDebugLogPath(path)
}

// debugLogOpenErrorIsSymlink always reports false: there is no portable errno
// for "refused to follow a symlink" to recognize here.
func debugLogOpenErrorIsSymlink(error) bool { return false }
