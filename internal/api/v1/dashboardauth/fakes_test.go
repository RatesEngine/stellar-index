package dashboardauth

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/StellarAtlas/stellar-atlas/internal/platform"
)

// In-process fakes for the three platform stores. Behavioural
// fidelity tracks what the Postgres impls do — sentinel-error
// shapes, atomic-consume semantics, idempotent revoke — so tests
// catch regressions a Postgres swap would also reject.

type fakeAccountStore struct {
	mu       sync.Mutex
	byID     map[uuid.UUID]platform.Account
	bySlug   map[string]platform.Account
	byStripe map[string]platform.Account
}

func newFakeAccountStore() *fakeAccountStore {
	return &fakeAccountStore{
		byID:     map[uuid.UUID]platform.Account{},
		bySlug:   map[string]platform.Account{},
		byStripe: map[string]platform.Account{},
	}
}

func (f *fakeAccountStore) Create(_ context.Context, a platform.Account) (platform.Account, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, exists := f.bySlug[a.Slug]; exists {
		return platform.Account{}, platform.ErrConflict
	}
	a.ID = uuid.New()
	a.CreatedAt = time.Now().UTC()
	f.byID[a.ID] = a
	f.bySlug[a.Slug] = a
	if a.StripeCustomerID != "" {
		f.byStripe[a.StripeCustomerID] = a
	}
	return a, nil
}

func (f *fakeAccountStore) Get(_ context.Context, id uuid.UUID) (platform.Account, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.byID[id]
	if !ok {
		return platform.Account{}, platform.ErrNotFound
	}
	return a, nil
}

func (f *fakeAccountStore) GetBySlug(_ context.Context, slug string) (platform.Account, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.bySlug[slug]
	if !ok {
		return platform.Account{}, platform.ErrNotFound
	}
	return a, nil
}

func (f *fakeAccountStore) GetByStripeCustomerID(_ context.Context, sid string) (platform.Account, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.byStripe[sid]
	if !ok {
		return platform.Account{}, platform.ErrNotFound
	}
	return a, nil
}

func (f *fakeAccountStore) Update(_ context.Context, a platform.Account) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.byID[a.ID]; !ok {
		return platform.ErrNotFound
	}
	f.byID[a.ID] = a
	f.bySlug[a.Slug] = a
	return nil
}

func (f *fakeAccountStore) Suspend(_ context.Context, id uuid.UUID, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.byID[id]
	if !ok {
		return platform.ErrNotFound
	}
	a.Status = platform.AccountSuspended
	a.SuspendedAt = time.Now().UTC()
	a.SuspendedReason = reason
	f.byID[id] = a
	return nil
}

func (f *fakeAccountStore) Unsuspend(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.byID[id]
	if !ok {
		return platform.ErrNotFound
	}
	a.Status = platform.AccountActive
	a.SuspendedAt = time.Time{}
	a.SuspendedReason = ""
	f.byID[id] = a
	return nil
}

type fakeUserStore struct {
	mu       sync.Mutex
	users    map[uuid.UUID]platform.User
	sessions map[uuid.UUID]platform.Session
}

func newFakeUserStore() *fakeUserStore {
	return &fakeUserStore{
		users:    map[uuid.UUID]platform.User{},
		sessions: map[uuid.UUID]platform.Session{},
	}
}

func (f *fakeUserStore) CreateUser(_ context.Context, u platform.User) (platform.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, existing := range f.users {
		if existing.Email == u.Email {
			return platform.User{}, platform.ErrConflict
		}
	}
	u.ID = uuid.New()
	u.CreatedAt = time.Now().UTC()
	f.users[u.ID] = u
	return u, nil
}

func (f *fakeUserStore) GetUserByID(_ context.Context, id uuid.UUID) (platform.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[id]
	if !ok {
		return platform.User{}, platform.ErrNotFound
	}
	return u, nil
}

func (f *fakeUserStore) GetUserByEmail(_ context.Context, email string) (platform.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, u := range f.users {
		if u.Email == email {
			return u, nil
		}
	}
	return platform.User{}, platform.ErrNotFound
}

func (f *fakeUserStore) ListUsersForAccount(_ context.Context, accountID uuid.UUID) ([]platform.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []platform.User
	for _, u := range f.users {
		if u.AccountID == accountID {
			out = append(out, u)
		}
	}
	return out, nil
}

func (f *fakeUserStore) UpdateUser(_ context.Context, u platform.User) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.users[u.ID]; !ok {
		return platform.ErrNotFound
	}
	f.users[u.ID] = u
	return nil
}

func (f *fakeUserStore) CreateSession(_ context.Context, s platform.Session) (platform.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s.ID = uuid.New()
	now := time.Now().UTC()
	s.CreatedAt = now
	s.LastSeenAt = now
	f.sessions[s.ID] = s
	return s, nil
}

func (f *fakeUserStore) GetSession(_ context.Context, id uuid.UUID) (platform.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sessions[id]
	if !ok || !s.RevokedAt.IsZero() {
		return platform.Session{}, platform.ErrNotFound
	}
	return s, nil
}

func (f *fakeUserStore) TouchSession(_ context.Context, id uuid.UUID, ip net.IP, ua string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sessions[id]
	if !ok || !s.RevokedAt.IsZero() {
		return platform.ErrNotFound
	}
	s.LastSeenAt = time.Now().UTC()
	if ip != nil {
		s.IPLastSeen = ip
	}
	if ua != "" {
		s.UserAgent = ua
	}
	f.sessions[id] = s
	return nil
}

func (f *fakeUserStore) RevokeSession(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sessions[id]
	if !ok || !s.RevokedAt.IsZero() {
		return nil // idempotent
	}
	s.RevokedAt = time.Now().UTC()
	f.sessions[id] = s
	return nil
}

func (f *fakeUserStore) RevokeAllUserSessions(_ context.Context, userID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now().UTC()
	for id, s := range f.sessions {
		if s.UserID == userID && s.RevokedAt.IsZero() {
			s.RevokedAt = now
			f.sessions[id] = s
		}
	}
	return nil
}

type fakeTokenStore struct {
	mu      sync.Mutex
	tokens  map[string]platform.MagicLinkToken // hex(hash) → row
	invites map[string]platform.Invite
	now     func() time.Time
}

func newFakeTokenStore(now func() time.Time) *fakeTokenStore {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &fakeTokenStore{
		tokens:  map[string]platform.MagicLinkToken{},
		invites: map[string]platform.Invite{},
		now:     now,
	}
}

func tokenKey(hash []byte) string {
	const hexDigits = "0123456789abcdef"
	out := make([]byte, len(hash)*2)
	for i, b := range hash {
		out[i*2] = hexDigits[b>>4]
		out[i*2+1] = hexDigits[b&0x0f]
	}
	return string(out)
}

func (f *fakeTokenStore) CreateMagicLinkToken(_ context.Context, t platform.MagicLinkToken) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	t.CreatedAt = f.now()
	f.tokens[tokenKey(t.TokenHash)] = t
	return nil
}

func (f *fakeTokenStore) ConsumeMagicLinkToken(_ context.Context, hash []byte) (platform.MagicLinkToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := tokenKey(hash)
	t, ok := f.tokens[k]
	if !ok || !t.ConsumedAt.IsZero() {
		return platform.MagicLinkToken{}, platform.ErrNotFound
	}
	if !t.ExpiresAt.After(f.now()) {
		return platform.MagicLinkToken{}, platform.ErrTokenExpired
	}
	t.ConsumedAt = f.now()
	f.tokens[k] = t
	return t, nil
}

func (f *fakeTokenStore) CreateInvite(_ context.Context, i platform.Invite) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	i.CreatedAt = f.now()
	f.invites[tokenKey(i.TokenHash)] = i
	return nil
}

func (f *fakeTokenStore) AcceptInvite(_ context.Context, hash []byte) (platform.Invite, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := tokenKey(hash)
	i, ok := f.invites[k]
	if !ok || !i.AcceptedAt.IsZero() || !i.RevokedAt.IsZero() {
		return platform.Invite{}, platform.ErrNotFound
	}
	if !i.ExpiresAt.After(f.now()) {
		return platform.Invite{}, platform.ErrTokenExpired
	}
	i.AcceptedAt = f.now()
	f.invites[k] = i
	return i, nil
}

func (f *fakeTokenStore) RevokeInvite(_ context.Context, hash []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := tokenKey(hash)
	i, ok := f.invites[k]
	if !ok {
		return platform.ErrNotFound
	}
	i.RevokedAt = f.now()
	f.invites[k] = i
	return nil
}

func (f *fakeTokenStore) ListInvitesForAccount(_ context.Context, accountID uuid.UUID) ([]platform.Invite, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []platform.Invite
	for _, i := range f.invites {
		if i.AccountID != accountID || !i.AcceptedAt.IsZero() || !i.RevokedAt.IsZero() {
			continue
		}
		out = append(out, i)
	}
	return out, nil
}

// Compile-time interface checks — keep the fakes honest if a
// real interface gains a method.
var (
	_ platform.AccountStore = (*fakeAccountStore)(nil)
	_ platform.UserStore    = (*fakeUserStore)(nil)
	_ platform.TokenStore   = (*fakeTokenStore)(nil)
)
