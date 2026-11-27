package httpapi

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/krunpy0/company-search/backend/internal/quota"
	"golang.org/x/crypto/bcrypt"
)

const (
	passwordCost   = 12
	sessionHashKey = "account.session_hash"
)

func accountEmail(raw string) (string, bool) {
	email := strings.ToLower(strings.TrimSpace(raw))
	address, err := mail.ParseAddress(email)
	return email, len(email) <= 254 && err == nil && address.Address == email
}

func accountPassword(password string) bool {
	return len(password) >= 12 && len(password) <= 72
}

func RegisterAuth(r *gin.Engine, pool *pgxpool.Pool) gin.HandlerFunc {
	return RegisterAuthWithLimiters(r, pool, nil, nil)
}

func RegisterAuthWithLimiters(r *gin.Engine, pool *pgxpool.Pool, limiters *RateLimiters, sendReset PasswordResetSender) gin.HandlerFunc {
	if limiters == nil {
		limiters = NewRateLimiters(DefaultRateLimitConfig())
	}
	dummyHash, err := bcrypt.GenerateFromPassword([]byte(rand.Text()), passwordCost)
	if err != nil {
		panic(err)
	}
	group := r.Group("/v1/auth")
	group.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
	})
	credentialSlots := make(chan struct{}, 4)
	credentials := func(c *gin.Context) (email, password string, ok bool) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
		var input struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if c.ShouldBindJSON(&input) == nil {
			email, validEmail := accountEmail(input.Email)
			if validEmail && accountPassword(input.Password) {
				return email, input.Password, true
			}
		}
		fail(c, 400, "invalid_credentials", "Provide an email and a password of 12 to 72 bytes")
		return "", "", false
	}
	bounded := func(c *gin.Context) {
		select {
		case credentialSlots <- struct{}{}:
			defer func() { <-credentialSlots }()
			c.Next()
		default:
			c.Header("Retry-After", "1")
			fail(c, 429, "busy", "Too many concurrent authentication requests")
		}
	}
	registerPasswordReset(group, pool, limiters.Auth(), bounded, sendReset)
	group.POST("/register", limiters.Auth(), bounded, func(c *gin.Context) {
		email, password, ok := credentials(c)
		if !ok {
			return
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(password), passwordCost)
		if err != nil {
			fail(c, 503, "auth_unavailable", "Authentication temporarily unavailable")
			return
		}
		var id int64
		err = pool.QueryRow(c.Request.Context(), `
			INSERT INTO accounts.users (email, password_hash) VALUES ($1, $2) RETURNING id`, email, hash).Scan(&id)
		var pgErr *pgconn.PgError
		switch {
		case errors.As(err, &pgErr) && pgErr.Code == "23505":
			fail(c, 409, "email_in_use", "Email already registered")
		case err != nil:
			fail(c, 503, "auth_unavailable", "Authentication temporarily unavailable")
		default:
			c.JSON(201, gin.H{"user_id": strconv.FormatInt(id, 10)})
		}
	})
	group.POST("/login", limiters.Auth(), bounded, func(c *gin.Context) {
		email, password, ok := credentials(c)
		if !ok {
			return
		}
		ctx := c.Request.Context()
		var id int64
		var hash []byte
		err := pool.QueryRow(ctx, `SELECT id, password_hash FROM accounts.users WHERE email=$1`, email).Scan(&id, &hash)
		if errors.Is(err, pgx.ErrNoRows) {
			hash = dummyHash
		} else if err != nil {
			fail(c, 503, "auth_unavailable", "Authentication temporarily unavailable")
			return
		}
		if bcrypt.CompareHashAndPassword(hash, []byte(password)) != nil || id == 0 {
			fail(c, 401, "invalid_credentials", "Invalid email or password")
			return
		}
		var secret [32]byte
		rand.Read(secret[:])
		token := base64.RawURLEncoding.EncodeToString(secret[:])
		tokenHash := sha256.Sum256(secret[:])
		var expiresAt time.Time
		// Serialize session issuance with credential changes and recheck the verified hash.
		err = pool.QueryRow(ctx, `
			INSERT INTO accounts.sessions (token_hash, user_id, expires_at)
			SELECT $1, id, statement_timestamp() + interval '24 hours'
			FROM accounts.users WHERE id=$2 AND email=$3 AND password_hash=$4 FOR SHARE
			RETURNING expires_at`, tokenHash[:], id, email, hash).Scan(&expiresAt)
		if errors.Is(err, pgx.ErrNoRows) {
			fail(c, 401, "invalid_credentials", "Invalid email or password")
			return
		}
		if err != nil {
			fail(c, 503, "auth_unavailable", "Authentication temporarily unavailable")
			return
		}
		c.JSON(200, gin.H{"access_token": token, "token_type": "Bearer", "expires_at": expiresAt.UTC()})
	})

	session := func(c *gin.Context, query string) {
		c.Header("Cache-Control", "no-store")
		parts := strings.Fields(c.GetHeader("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || len(parts[1]) != 43 {
			rejectSession(c)
			return
		}
		secret, err := base64.RawURLEncoding.Strict().DecodeString(parts[1])
		if err != nil {
			rejectSession(c)
			return
		}
		hash := sha256.Sum256(secret)
		var id int64
		err = pool.QueryRow(c.Request.Context(), query, hash[:]).Scan(&id)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			rejectSession(c)
		case err != nil:
			fail(c, 503, "auth_unavailable", "Authentication temporarily unavailable")
		default:
			c.Request = c.Request.WithContext(quota.WithUser(c.Request.Context(), id))
			c.Set(sessionHashKey, hash)
		}
	}
	group.POST("/logout", limiters.API(), func(c *gin.Context) {
		session(c, `DELETE FROM accounts.sessions WHERE token_hash=$1 AND expires_at > statement_timestamp() RETURNING user_id`)
		if !c.IsAborted() {
			c.Status(204)
		}
	})
	requireSession := func(c *gin.Context) {
		session(c, `SELECT user_id FROM accounts.sessions WHERE token_hash=$1 AND expires_at > statement_timestamp()`)
	}
	registerAccount(r.Group("/v1/account", requireSession, limiters.API()), pool, bounded)
	return requireSession
}

func rejectSession(c *gin.Context) {
	c.Header("WWW-Authenticate", "Bearer")
	fail(c, 401, "invalid_session", "A valid session is required")
}
