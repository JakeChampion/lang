package interp

import (
	"reflect"
	"testing"
)

// parseMountinfo against the shapes mountinfo actually writes: optional tags
// of varying count before the `-`, octal escapes in the mount point, a minor
// number past 255, and a line too short to be a row.
func TestParseMountinfo(t *testing.T) {
	const text = "22 1 8:1 / / rw,relatime shared:1 - ext4 /dev/sda1 rw\n" +
		"35 22 0:31 / /mnt/with\\040space rw - tmpfs tmp\\134fs rw,size=64k\n" +
		"40 22 259:300 / /data rw,nosuid shared:7 master:2 - xfs /dev/nvme0n1p300 rw\n" +
		"bogus line\n"
	want := []rawMount{
		{source: "/dev/sda1", target: "/", fstype: "ext4", dev: 8<<8 | 1},
		{source: `tmp\fs`, target: "/mnt/with space", fstype: "tmpfs", dev: 31},
		{source: "/dev/nvme0n1p300", target: "/data", fstype: "xfs", dev: 300&0xff | 259<<8 | (300&^0xff)<<12},
	}
	if got := parseMountinfo(text); !reflect.DeepEqual(got, want) {
		t.Errorf("parseMountinfo:\n got %+v\nwant %+v", got, want)
	}
}
