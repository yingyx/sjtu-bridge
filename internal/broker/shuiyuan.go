package broker

import (
	"context"
	"encoding/json"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/storage"
)

type categoryVerification struct {
	credential storage.Credential
	categories []contracts.ShuiyuanCategory
}

func (v categoryVerification) payload() map[string]any {
	refs := []string{}
	for _, category := range v.categories {
		refs = append(refs, category.ResourceRef)
	}
	return map[string]any{"account_ref": v.credential.AccountRef, "resource_refs": refs, "categories": v.categories, "scope": "visible_site_category_snapshot_only"}
}

// shuiyuanResources discovers explicit grant targets for the owner. Results
// cross the same credential-generation publication fence as other owner reads.
func (b *Broker) shuiyuanResources(ctx context.Context, c storage.Credential) (any, error) {
	if e := b.Store.CheckCredential(c); e != nil {
		return nil, e
	}
	provider, ok := b.Engine.CampusProviders[c.Provider].(interface {
		Categories(context.Context, []byte) (string, []contracts.ShuiyuanCategory, error)
	})
	if !ok {
		return nil, contracts.Fail("UNSUPPORTED", "Category discovery is unavailable")
	}
	var categories []contracts.ShuiyuanCategory
	e := b.Engine.Do(ctx, func(ctx context.Context) error {
		secret, e := b.Store.CheckedCredentialSecret(c)
		if e != nil {
			return e
		}
		defer clear(secret)
		actual, rows, e := provider.Categories(ctx, secret)
		if e != nil {
			return e
		}
		if actual != c.AccountRef {
			return contracts.Fail("PERMISSION_DENIED", "Saved forum identity changed")
		}
		categories = rows
		return nil
	})
	if e != nil {
		return nil, e
	}
	result := categoryVerification{credential: c, categories: categories}
	raw, e := json.Marshal(result.payload())
	if e != nil || len(raw) > contracts.MaxJSONBytes {
		return nil, contracts.Fail("RESPONSE_TOO_LARGE", "Narrow category discovery before retrying")
	}
	if e = b.Store.CheckCredential(c); e != nil {
		return nil, e
	}
	return result, nil
}
