package engine

import "github.com/yingyx/sjtu-bridge/contracts"

func projectShuiyuan(q contracts.CampusQuery, r contracts.CampusRecord) (contracts.CampusRecord, error) {
	if r.Title == "" || !contracts.ValidShuiyuanHit(r.Shuiyuan, q.ResourceRef) || r.Ref != r.Shuiyuan.PostRef {
		return contracts.CampusRecord{}, contracts.Fail("UPSTREAM_CHANGED", "Invalid fixed forum search view")
	}
	hit := *r.Shuiyuan
	return contracts.CampusRecord{ResourceRef: q.ResourceRef, Ref: r.Ref, Title: r.Title, Shuiyuan: &hit}, nil
}
