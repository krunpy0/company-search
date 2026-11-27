package httpapi

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/krunpy0/company-search/backend/internal/quota"
	"golang.org/x/crypto/bcrypt"
)

type accountProfile struct {
	UserID      int64  `json:"user_id,string"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	HasAvatar   bool   `json:"has_avatar"`
}

const profileColumns = "id, email, display_name, avatar IS NOT NULL"

var errAccountPassword = errors.New("incorrect account password")

func readAccountProfile(row pgx.Row) (accountProfile, error) {
	var profile accountProfile
	err := row.Scan(&profile.UserID, &profile.Email, &profile.DisplayName, &profile.HasAvatar)
	return profile, err
}

func registerAccount(group *gin.RouterGroup, pool *pgxpool.Pool, bounded gin.HandlerFunc) {
	group.GET("/profile", func(c *gin.Context) {
		user, _ := quota.UserID(c.Request.Context())
		profile, err := readAccountProfile(pool.QueryRow(c.Request.Context(),
			"SELECT "+profileColumns+" FROM accounts.users WHERE id=$1", user))
		if err != nil {
			accountFailure(c, err)
			return
		}
		c.JSON(200, profile)
	})
	group.PATCH("/profile", bounded, func(c *gin.Context) {
		var input struct {
			DisplayName     *string `json:"display_name"`
			Email           *string `json:"email"`
			CurrentPassword string  `json:"current_password"`
		}
		if !chatJSON(c, &input) {
			return
		}
		if input.DisplayName == nil && input.Email == nil {
			fail(c, 400, "invalid_profile", "Provide a display name or email")
			return
		}
		if input.DisplayName != nil {
			name := strings.TrimSpace(*input.DisplayName)
			if utf8.RuneCountInString(name) > 80 || strings.IndexFunc(name, unicode.IsControl) >= 0 {
				fail(c, 400, "invalid_profile", "Provide a display name of at most 80 characters")
				return
			}
			input.DisplayName = &name
		}
		ctx := c.Request.Context()
		user, _ := quota.UserID(ctx)
		var profile accountProfile
		var err error
		if input.Email == nil {
			profile, err = readAccountProfile(pool.QueryRow(ctx,
				"UPDATE accounts.users SET display_name=$2 WHERE id=$1 RETURNING "+profileColumns, user, input.DisplayName))
		} else {
			email, ok := accountEmail(*input.Email)
			if !ok {
				fail(c, 400, "invalid_profile", "Provide a valid email address")
				return
			}
			if !checkPasswordInput(c, input.CurrentPassword) {
				return
			}
			err = withAccountPassword(c, pool, user, input.CurrentPassword, func(tx pgx.Tx, _ []byte) error {
				updated, err := readAccountProfile(tx.QueryRow(ctx, `UPDATE accounts.users SET email=$2,
					reset_token_hash=NULL, reset_expires_at=NULL,
					display_name=COALESCE($3, display_name) WHERE id=$1 RETURNING `+profileColumns,
					user, email, input.DisplayName))
				profile = updated
				return err
			})
		}
		if err != nil {
			accountFailure(c, err)
			return
		}
		c.JSON(200, profile)
	})
	group.POST("/password", bounded, func(c *gin.Context) {
		var input struct {
			CurrentPassword string `json:"current_password"`
			NewPassword     string `json:"new_password"`
		}
		if !chatJSON(c, &input) || !checkPasswordInput(c, input.CurrentPassword) || !checkPasswordInput(c, input.NewPassword) {
			return
		}
		ctx := c.Request.Context()
		user, _ := quota.UserID(ctx)
		err := withAccountPassword(c, pool, user, input.CurrentPassword, func(tx pgx.Tx, session []byte) error {
			hash, err := bcrypt.GenerateFromPassword([]byte(input.NewPassword), passwordCost)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE accounts.users SET password_hash=$2,
				reset_token_hash=NULL, reset_expires_at=NULL WHERE id=$1`, user, hash); err != nil {
				return err
			}
			_, err = tx.Exec(ctx, "DELETE FROM accounts.sessions WHERE user_id=$1 AND token_hash<>$2", user, session)
			return err
		})
		if err != nil {
			accountFailure(c, err)
			return
		}
		c.Status(204)
	})
	group.DELETE("", bounded, func(c *gin.Context) {
		var input struct {
			CurrentPassword string `json:"current_password"`
		}
		if !chatJSON(c, &input) || !checkPasswordInput(c, input.CurrentPassword) {
			return
		}
		ctx := c.Request.Context()
		user, _ := quota.UserID(ctx)
		err := withAccountPassword(c, pool, user, input.CurrentPassword, func(tx pgx.Tx, _ []byte) error {
			_, err := tx.Exec(ctx, "DELETE FROM accounts.users WHERE id=$1", user)
			return err
		})
		if err != nil {
			accountFailure(c, err)
			return
		}
		c.Status(204)
	})
	registerAccountAvatar(group, pool)
}

func checkPasswordInput(c *gin.Context, value string) bool {
	if accountPassword(value) {
		return true
	}
	fail(c, 400, "invalid_password", "Provide passwords of 12 to 72 bytes")
	return false
}

func withAccountPassword(c *gin.Context, pool *pgxpool.Pool, user int64, current string, mutate func(pgx.Tx, []byte) error) error {
	ctx := c.Request.Context()
	sessionHash := c.MustGet(sessionHashKey).([32]byte)
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		var hash []byte
		err := tx.QueryRow(ctx, "SELECT password_hash FROM accounts.users WHERE id=$1 FOR UPDATE", user).Scan(&hash)
		if err != nil {
			return err
		}
		// Check again after acquiring the account lock. Another password change may have revoked this session.
		var active int
		err = tx.QueryRow(ctx, `SELECT 1 FROM accounts.sessions
			WHERE token_hash=$1 AND user_id=$2 AND expires_at > statement_timestamp()`, sessionHash[:], user).Scan(&active)
		if err != nil {
			return err
		}
		if bcrypt.CompareHashAndPassword(hash, []byte(current)) != nil {
			return errAccountPassword
		}
		return mutate(tx, sessionHash[:])
	})
}

func accountFailure(c *gin.Context, err error) {
	var pgErr *pgconn.PgError
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		rejectSession(c)
	case errors.Is(err, errAccountPassword):
		fail(c, 403, "incorrect_password", "The current password is incorrect")
	case errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "users_email_key":
		fail(c, 409, "email_in_use", "Email already registered")
	default:
		fail(c, 503, "auth_unavailable", "Authentication temporarily unavailable")
	}
}
