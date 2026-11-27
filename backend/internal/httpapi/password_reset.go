package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

type PasswordResetSender func(ctx context.Context, email, token string) error

func registerPasswordReset(group *gin.RouterGroup, pool *pgxpool.Pool, limit, bounded gin.HandlerFunc, send PasswordResetSender) {
	mailSlots := make(chan struct{}, 4)
	unavailable := func(c *gin.Context) {
		fail(c, 503, "auth_unavailable", "Password reset temporarily unavailable")
	}
	group.POST("/password-reset/request", limit, func(c *gin.Context) {
		if send == nil {
			unavailable(c)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
		var input struct {
			Email string `json:"email"`
		}
		if c.ShouldBindJSON(&input) != nil {
			fail(c, 400, "invalid_email", "Provide a valid email address")
			return
		}
		email, ok := accountEmail(input.Email)
		if !ok {
			fail(c, 400, "invalid_email", "Provide a valid email address")
			return
		}
		select {
		case mailSlots <- struct{}{}:
		default:
			c.Header("Retry-After", "5")
			fail(c, 429, "busy", "Too many concurrent password reset requests")
			return
		}

		var secret [32]byte
		rand.Read(secret[:])
		hash := sha256.Sum256(secret[:])
		result, err := pool.Exec(c.Request.Context(), `
			UPDATE accounts.users SET reset_token_hash=$2,
				reset_expires_at=statement_timestamp() + interval '30 minutes'
			WHERE email=$1 AND (reset_expires_at IS NULL OR reset_expires_at <= statement_timestamp())`, email, hash[:])
		if err != nil {
			<-mailSlots
			unavailable(c)
			return
		}
		if result.RowsAffected() == 0 {
			<-mailSlots
		} else {
			// Mail-provider latency must not reveal whether the address is registered.
			go func() {
				defer func() { <-mailSlots }()
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := send(ctx, email, base64.RawURLEncoding.EncodeToString(secret[:])); err != nil {
					slog.Warn("password reset email failed", "error", err)
					cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					defer cancel()
					if _, err := pool.Exec(cleanupCtx, `UPDATE accounts.users SET
						reset_token_hash=NULL, reset_expires_at=NULL WHERE reset_token_hash=$1`, hash[:]); err != nil {
						slog.Warn("password reset token cleanup failed")
					}
				}
			}()
		}
		c.Status(202)
	})
	group.POST("/password-reset/confirm", limit, bounded, func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
		var input struct {
			Token    string `json:"token"`
			Password string `json:"password"`
		}
		if c.ShouldBindJSON(&input) != nil {
			fail(c, 400, "invalid_input", "Provide the reset token and a new password")
			return
		}
		if !checkPasswordInput(c, input.Password) {
			return
		}
		invalidToken := func() {
			fail(c, 400, "invalid_reset_token", "The reset link is invalid or expired")
		}
		secret, err := base64.RawURLEncoding.Strict().DecodeString(input.Token)
		if err != nil || len(input.Token) != 43 || len(secret) != 32 {
			invalidToken()
			return
		}
		hash := sha256.Sum256(secret)
		ctx := c.Request.Context()
		err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
			var userID int64
			err := tx.QueryRow(ctx, `SELECT id FROM accounts.users WHERE reset_token_hash=$1
				AND reset_expires_at > clock_timestamp() FOR UPDATE`, hash[:]).Scan(&userID)
			if err != nil {
				return err
			}
			passwordHash, err := bcrypt.GenerateFromPassword([]byte(input.Password), passwordCost)
			if err != nil {
				return err
			}
			result, err := tx.Exec(ctx, `UPDATE accounts.users SET password_hash=$2,
				reset_token_hash=NULL, reset_expires_at=NULL WHERE id=$1 AND reset_expires_at > clock_timestamp()`, userID, passwordHash)
			if err != nil {
				return err
			}
			if result.RowsAffected() == 0 {
				return pgx.ErrNoRows
			}
			_, err = tx.Exec(ctx, "DELETE FROM accounts.sessions WHERE user_id=$1", userID)
			return err
		})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			invalidToken()
		case err != nil:
			unavailable(c)
		default:
			c.Status(204)
		}
	})
}
