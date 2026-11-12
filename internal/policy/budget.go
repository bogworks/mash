package policy

// Limits the JWTs mash makes must fit.
const (
	// AccountJWTLimit is the largest account claims nats-server accepts: 1 MiB. It is the jwt library's decoder cap
	// (jwt.MaxTokenSize), which the server applies to every account JWT whatever max_payload says.
	AccountJWTLimit = 1 << 20
	// UserJWTLimit is the largest user JWT mash allows: a user's JWT travels in the CONNECT line, which the default
	// server caps at 4096 bytes together with the signature, name and options. Measured: a 3,802 byte JWT
	// connects and a 3,838 byte one fails (#179), so the limit leaves real headroom below the failing size.
	UserJWTLimit = 3750
)
