package assets

import (
	"context"
	"strings"
	"time"

	"github.com/worxbend/twi/internal/storage"
	"github.com/worxbend/twi/internal/twitch"
)

const defaultAvatarMetadataTTL = 24 * time.Hour

// AvatarLookup resolves Twitch user metadata in batches.
type AvatarLookup interface {
	GetUsers(context.Context, twitch.UserLookupRequest) ([]twitch.UserIdentity, error)
}

// AvatarRequest identifies a chat author whose avatar URL may need metadata
// lookup. The fallback chip is derived from existing message author fields,
// not from lookup results, so metadata resolution cannot reflow text rows.
type AvatarRequest struct {
	UserID      string
	UserLogin   string
	DisplayName string
}

// AvatarResult is the cached or resolved avatar metadata for one author.
type AvatarResult struct {
	UserID      string
	UserLogin   string
	DisplayName string
	AvatarURL   string
	FromCache   bool
	Found       bool
}

// AvatarBatchResolver resolves visible chat author avatars through a cache
// first, then one batched Twitch user lookup for all misses.
type AvatarBatchResolver struct {
	Lookup AvatarLookup
	Cache  storage.AssetCache
	Now    func() time.Time
	TTL    time.Duration
}

// ResolveAvatars resolves unique authors without issuing per-message API
// calls. Cache hits are returned before any network lookup is attempted.
func (r *AvatarBatchResolver) ResolveAvatars(ctx context.Context, requests []AvatarRequest) ([]AvatarResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	now := r.now()
	ttl := r.ttl()
	unique := uniqueAvatarRequests(requests)

	results := make([]AvatarResult, 0, len(unique))
	misses := make([]AvatarRequest, 0, len(unique))
	for _, req := range unique {
		record, ok, err := r.cachedAvatar(ctx, req, now)
		if err != nil {
			return results, err
		}
		if ok {
			results = append(results, AvatarResult{
				UserID:      userIDFromCacheKey(record.Key, req.UserID),
				UserLogin:   req.UserLogin,
				DisplayName: req.DisplayName,
				AvatarURL:   record.SourceURL,
				FromCache:   true,
				Found:       record.SourceURL != "",
			})
			continue
		}
		misses = append(misses, req)
	}
	if len(misses) == 0 || r == nil || r.Lookup == nil {
		return results, ctx.Err()
	}

	lookupReq := twitch.UserLookupRequest{
		UserIDs:    make([]string, 0, len(misses)),
		UserLogins: make([]string, 0, len(misses)),
	}
	for _, miss := range misses {
		if strings.TrimSpace(miss.UserID) != "" {
			lookupReq.UserIDs = append(lookupReq.UserIDs, miss.UserID)
			continue
		}
		if strings.TrimSpace(miss.UserLogin) != "" {
			lookupReq.UserLogins = append(lookupReq.UserLogins, miss.UserLogin)
		}
	}

	users, err := r.Lookup.GetUsers(ctx, lookupReq)
	if err != nil {
		return results, err
	}
	byID := make(map[string]twitch.UserIdentity, len(users))
	byLogin := make(map[string]twitch.UserIdentity, len(users))
	for _, user := range users {
		if strings.TrimSpace(user.UserID) != "" {
			byID[user.UserID] = user
		}
		if login := canonicalLogin(user.Login); login != "" {
			byLogin[login] = user
		}
	}

	for _, miss := range misses {
		user, ok := userForAvatarRequest(miss, byID, byLogin)
		if !ok || strings.TrimSpace(user.ProfileImageURL) == "" {
			results = append(results, AvatarResult{
				UserID:      miss.UserID,
				UserLogin:   miss.UserLogin,
				DisplayName: miss.DisplayName,
				Found:       false,
			})
			continue
		}
		result := AvatarResult{
			UserID:      firstNonEmpty(user.UserID, miss.UserID),
			UserLogin:   firstNonEmpty(user.Login, miss.UserLogin),
			DisplayName: firstNonEmpty(user.DisplayName, miss.DisplayName),
			AvatarURL:   user.ProfileImageURL,
			Found:       true,
		}
		results = append(results, result)
		_ = r.putAvatar(ctx, miss, result, now, ttl)
	}
	return results, ctx.Err()
}

func (r *AvatarBatchResolver) cachedAvatar(ctx context.Context, req AvatarRequest, now time.Time) (storage.AssetRecord, bool, error) {
	if r == nil || r.Cache == nil {
		return storage.AssetRecord{}, false, nil
	}
	key := AvatarCacheKey(req)
	if key.ID == "" {
		return storage.AssetRecord{}, false, nil
	}
	record, ok, err := r.Cache.GetAsset(ctx, key)
	if err != nil || !ok {
		return storage.AssetRecord{}, false, err
	}
	if !cacheRecordFresh(record, now) || strings.TrimSpace(record.SourceURL) == "" {
		return storage.AssetRecord{}, false, nil
	}
	return record, true, nil
}

func (r *AvatarBatchResolver) putAvatar(ctx context.Context, req AvatarRequest, result AvatarResult, now time.Time, ttl time.Duration) error {
	if r == nil || r.Cache == nil || strings.TrimSpace(result.AvatarURL) == "" {
		return nil
	}
	record := storage.AssetRecord{
		Key:       AvatarCacheKey(AvatarRequest{UserID: firstNonEmpty(result.UserID, req.UserID), UserLogin: firstNonEmpty(result.UserLogin, req.UserLogin)}),
		SourceURL: result.AvatarURL,
		MediaType: "image/png",
		FetchedAt: now,
		ExpiresAt: now.Add(ttl),
	}
	if record.Key.ID != "" {
		record = r.preserveDownloadedAvatar(ctx, record)
		if err := r.Cache.PutAsset(ctx, record); err != nil {
			return err
		}
	}
	loginKey := AvatarCacheKey(AvatarRequest{UserLogin: firstNonEmpty(result.UserLogin, req.UserLogin)})
	if loginKey.ID != "" && loginKey != record.Key {
		record.Key = loginKey
		record = r.preserveDownloadedAvatar(ctx, record)
		if err := r.Cache.PutAsset(ctx, record); err != nil {
			return err
		}
	}
	return nil
}

func (r *AvatarBatchResolver) preserveDownloadedAvatar(ctx context.Context, record storage.AssetRecord) storage.AssetRecord {
	if r == nil || r.Cache == nil || record.Key.ID == "" {
		return record
	}
	existing, ok, err := r.Cache.GetAsset(ctx, record.Key)
	if err != nil || !ok || strings.TrimSpace(existing.Path) == "" {
		return record
	}
	if strings.TrimSpace(existing.SourceURL) != strings.TrimSpace(record.SourceURL) {
		return record
	}
	record.Path = existing.Path
	record.PayloadIdentity = existing.PayloadIdentity
	if record.MediaType == "" {
		record.MediaType = existing.MediaType
	}
	if record.WidthCells <= 0 {
		record.WidthCells = existing.WidthCells
	}
	if record.HeightCells <= 0 {
		record.HeightCells = existing.HeightCells
	}
	return record
}

func (r *AvatarBatchResolver) now() time.Time {
	if r != nil && r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *AvatarBatchResolver) ttl() time.Duration {
	if r != nil && r.TTL > 0 {
		return r.TTL
	}
	return defaultAvatarMetadataTTL
}

// AvatarCacheKey returns the URL-free cache key for avatar metadata.
func AvatarCacheKey(req AvatarRequest) storage.AssetKey {
	if id := strings.TrimSpace(req.UserID); id != "" {
		if !unsafeAssetKeyPart(id) {
			return storage.AssetKey{Kind: KindAvatar, ID: id}
		}
	}
	if login := canonicalLogin(req.UserLogin); login != "" {
		id := "login/" + login
		if !unsafeAssetKeyPart(id) {
			return storage.AssetKey{Kind: KindAvatar, ID: id}
		}
	}
	if name := canonicalLogin(req.DisplayName); name != "" {
		id := "login/" + name
		if !unsafeAssetKeyPart(id) {
			return storage.AssetKey{Kind: KindAvatar, ID: id}
		}
	}
	return storage.AssetKey{Kind: KindAvatar}
}

func uniqueAvatarRequests(requests []AvatarRequest) []AvatarRequest {
	seen := make(map[storage.AssetKey]bool, len(requests))
	out := make([]AvatarRequest, 0, len(requests))
	for _, req := range requests {
		req.UserID = strings.TrimSpace(req.UserID)
		req.UserLogin = strings.TrimSpace(req.UserLogin)
		req.DisplayName = strings.TrimSpace(req.DisplayName)
		key := AvatarCacheKey(req)
		if key.ID == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, req)
	}
	return out
}

func userForAvatarRequest(req AvatarRequest, byID map[string]twitch.UserIdentity, byLogin map[string]twitch.UserIdentity) (twitch.UserIdentity, bool) {
	if req.UserID != "" {
		user, ok := byID[req.UserID]
		return user, ok
	}
	user, ok := byLogin[canonicalLogin(req.UserLogin)]
	if ok {
		return user, true
	}
	user, ok = byLogin[canonicalLogin(req.DisplayName)]
	return user, ok
}

func userIDFromCacheKey(key storage.AssetKey, fallback string) string {
	if strings.HasPrefix(key.ID, "login/") {
		return fallback
	}
	return firstNonEmpty(key.ID, fallback)
}

func canonicalLogin(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
