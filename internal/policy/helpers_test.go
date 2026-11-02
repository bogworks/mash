package policy

import (
	"github.com/nats-io/jwt/v2"

	"github.com/bogworks/mash/internal/admin"
)

func permsFor(recipients []string) jwt.Permissions { return admin.PermissionsTo("o", "x", recipients) }
