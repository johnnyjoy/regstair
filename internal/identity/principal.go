package identity

type Kind string

const (
	KindAnonymous        Kind = "anonymous"
	KindConfiguredClient Kind = "configured_client"
	KindLocalUser        Kind = "local_user"
)

type Principal struct {
	Kind     Kind
	ID       string
	Username string
}

func Anonymous() Principal { return Principal{Kind: KindAnonymous} }

func (p Principal) EventIdentity() string {
	if p.Kind == KindAnonymous {
		return ""
	}
	return p.ID
}
