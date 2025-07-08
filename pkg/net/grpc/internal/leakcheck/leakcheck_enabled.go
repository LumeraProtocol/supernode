//go:build checkbuffers

package leakcheck

func init() {
	failTestsOnLeakedBuffers = true
}
