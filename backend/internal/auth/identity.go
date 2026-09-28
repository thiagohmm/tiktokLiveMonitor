package auth

// The identity provider is Supabase Auth today and is planned to move into
// the backend (users in the operational Postgres). Callers depend on these
// interfaces, not on Supabase, so the provider can be swapped without
// touching the HTTP layer or the tenant model (organization_members.user_id
// is plain TEXT, without a foreign key to auth.users).

// Authenticator validates sessions and handles password logins.
type Authenticator interface {
	// ValidateToken returns the user of an access token.
	ValidateToken(token string) (*User, error)
	SignInWithPassword(email, password string) (*LoginSession, error)
	// SignOutGlobal revokes every session of the token's user.
	SignOutGlobal(accessToken string) error
	UpdatePassword(accessToken, newPassword string) error
}

// AccountDirectory manages accounts (platform admin and organization owners).
type AccountDirectory interface {
	ListSubscribers() ([]SubscriberProfile, error)
	GetProfileByID(id string) (*SubscriberProfile, error)
	CreateSubscriber(req CreateSubscriberRequest) (*SubscriberProfile, error)
	SignUpPending(req SignUpRequest) (*SubscriberProfile, error)
	UpdateSubscriber(req UpdateSubscriberRequest) (*SubscriberProfile, error)
	DeleteSubscriber(id string) error
	GenerateRecoveryLink(email, redirectTo string) (string, error)
}

var (
	_ Authenticator    = Config{}
	_ AccountDirectory = (*AdminClient)(nil)
)
