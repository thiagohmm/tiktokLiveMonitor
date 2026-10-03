package auth

// Authenticator validates local sessions and handles password operations.
type Authenticator interface {
	ValidateToken(token string) (*User, error)
	SignInWithPassword(email, password string) (*LoginSession, error)
	SignOutGlobal(token string) error
	UpdatePassword(token, newPassword string) error
}

// AccountDirectory manages local accounts without granting tenant privileges.
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
	_ AccountDirectory = (*Store)(nil)
)
