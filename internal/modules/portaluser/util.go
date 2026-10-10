package portaluser

import (
	"sort"
	"time"
)

// timeNow 便于测试替换。
var timeNow = time.Now

func fmtTs(sec int64) string {
	if sec <= 0 {
		return ""
	}
	return time.Unix(sec, 0).Format("2006-01-02 15:04:05")
}

func sortIPs(list []IPSeen) {
	sort.Slice(list, func(i, j int) bool {
		if list[i].SeenAt != list[j].SeenAt {
			return list[i].SeenAt > list[j].SeenAt
		}
		return list[i].IP < list[j].IP
	})
}
