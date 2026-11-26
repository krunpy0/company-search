package httpapi

import (
	"bytes"
	"errors"
	"image"
	_ "image/jpeg"
	"image/png"
	"io"
	"mime"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/krunpy0/company-search/backend/internal/quota"
)

const (
	maxAvatarBytes   = 2 << 20
	maxAvatarEdge    = 4096
	maxAvatarPixels  = 8 << 20
	storedAvatarEdge = 512
)

func registerAccountAvatar(group *gin.RouterGroup, pool *pgxpool.Pool) {
	group.GET("/avatar", func(c *gin.Context) {
		ctx := c.Request.Context()
		user, _ := quota.UserID(ctx)
		var avatar []byte
		err := pool.QueryRow(ctx, "SELECT avatar FROM accounts.users WHERE id=$1", user).Scan(&avatar)
		if err != nil {
			accountFailure(c, err)
			return
		}
		if avatar == nil {
			fail(c, 404, "avatar_not_found", "There is no saved avatar")
			return
		}
		c.Header("X-Content-Type-Options", "nosniff")
		c.Data(200, "image/png", avatar)
	})
	slots := make(chan struct{}, 2)
	group.PUT("/avatar", func(c *gin.Context) {
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			c.Header("Retry-After", "1")
			fail(c, 429, "busy", "Too many concurrent avatar uploads")
			return
		}

		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxAvatarBytes)
		raw, err := io.ReadAll(c.Request.Body)
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			fail(c, 413, "avatar_too_large", "Upload an avatar of at most 2 MiB")
			return
		}
		contentType, _, _ := mime.ParseMediaType(c.GetHeader("Content-Type"))
		avatar, ok := normalizeAvatar(raw, contentType)
		if err != nil || !ok {
			fail(c, 400, "invalid_avatar", "Upload a PNG or JPEG image with valid dimensions")
			return
		}
		ctx := c.Request.Context()
		user, _ := quota.UserID(ctx)
		profile, err := readAccountProfile(pool.QueryRow(ctx,
			"UPDATE accounts.users SET avatar=$2 WHERE id=$1 RETURNING "+profileColumns, user, avatar))
		if err != nil {
			accountFailure(c, err)
			return
		}
		c.JSON(200, profile)
	})
	group.DELETE("/avatar", func(c *gin.Context) {
		ctx := c.Request.Context()
		user, _ := quota.UserID(ctx)
		updated, err := pool.Exec(ctx, "UPDATE accounts.users SET avatar=NULL WHERE id=$1", user)
		switch {
		case err != nil:
			accountFailure(c, err)
		case updated.RowsAffected() == 0:
			accountFailure(c, pgx.ErrNoRows)
		default:
			c.Status(204)
		}
	})
}

func normalizeAvatar(raw []byte, contentType string) ([]byte, bool) {
	config, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || (contentType != "image/png" && contentType != "image/jpeg") || contentType != "image/"+format ||
		config.Width <= 0 || config.Height <= 0 || config.Width > maxAvatarEdge || config.Height > maxAvatarEdge ||
		config.Width*config.Height > maxAvatarPixels {
		return nil, false
	}
	decoded, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, false
	}
	bounds := decoded.Bounds()
	if bounds.Dx() != config.Width || bounds.Dy() != config.Height {
		return nil, false
	}
	largest := max(bounds.Dx(), bounds.Dy())
	edge := min(largest, storedAvatarEdge)
	width := max(1, bounds.Dx()*edge/largest)
	height := max(1, bounds.Dy()*edge/largest)
	avatar := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			avatar.Set(x, y, decoded.At(bounds.Min.X+x*bounds.Dx()/width, bounds.Min.Y+y*bounds.Dy()/height))
		}
	}
	var encoded bytes.Buffer
	if png.Encode(&encoded, avatar) != nil || encoded.Len() > maxAvatarBytes {
		return nil, false
	}
	return encoded.Bytes(), true
}
