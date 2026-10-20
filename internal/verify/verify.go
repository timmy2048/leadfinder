// Package verify wraps AfterShip/email-verifier.
package verify

import (
	"strings"

	emailverifier "github.com/AfterShip/email-verifier"
)

// Verifier checks syntax, MX, disposable and optionally SMTP.
// SMTP checks need outbound TCP port 25, which most home ISPs and cloud
// providers block; when blocked results become "unknown".
type Verifier struct {
	v    *emailverifier.Verifier
	smtp bool
}

func New(smtp bool) *Verifier {
	v := emailverifier.NewVerifier()
	if smtp {
		v = v.EnableSMTPCheck()
	}
	return &Verifier{v: v, smtp: smtp}
}

// Check returns valid | risky | invalid | unknown.
func (x *Verifier) Check(email string) string {
	r, err := x.v.Verify(email)
	if err != nil || r == nil {
		return "unknown"
	}
	if !r.Syntax.Valid {
		return "invalid"
	}
	if !r.HasMxRecords {
		return "invalid"
	}
	if r.Disposable {
		return "risky"
	}
	if x.smtp {
		if r.SMTP == nil {
			return "unknown"
		}
		if r.SMTP.CatchAll {
			return "risky"
		}
		if !r.SMTP.Deliverable {
			if r.SMTP.HostExists {
				return "invalid"
			}
			return "unknown"
		}
		return "valid"
	}
	if r.RoleAccount && strings.Contains(email, "noreply") {
		return "risky"
	}
	return "valid" // syntax+MX ok
}
