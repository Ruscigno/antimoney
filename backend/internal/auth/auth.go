package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/user/antimoney/internal/seed"
	"golang.org/x/crypto/bcrypt"
)

// ─── Context Keys ──────────────────────────────────────────────────────

type contextKey string

const (
	UserIDKey   contextKey = "user_id"
	BookGUIDKey contextKey = "book_guid"
)

// UserIDFromCtx extracts the authenticated user ID from context.
func UserIDFromCtx(ctx context.Context) string {
	v, _ := ctx.Value(UserIDKey).(string)
	return v
}

// BookGUIDFromCtx extracts the user's book GUID from context.
func BookGUIDFromCtx(ctx context.Context) string {
	v, _ := ctx.Value(BookGUIDKey).(string)
	return v
}

// ─── JWT ────────────────────────────────────────────────────────────────

var jwtSecret []byte

func SetJWTSecret(secret string) {
	jwtSecret = []byte(secret)
}

type Claims struct {
	UserID   string `json:"user_id"`
	BookGUID string `json:"book_guid"`
	Email    string `json:"email"`
	jwt.RegisteredClaims
}

// GenerateToken creates a JWT valid for 7 days.
func GenerateToken(userID, bookGUID, email string) (string, error) {
	claims := &Claims{
		UserID:   userID,
		BookGUID: bookGUID,
		Email:    email,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(7 * 24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(jwtSecret)
}

// ParseToken validates a JWT and returns the claims.
func ParseToken(tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return jwtSecret, nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid token")
	}
	return claims, nil
}

// ─── Middleware ──────────────────────────────────────────────────────────

// RequireAuth is middleware that validates the JWT in the Authorization header
// and sets user_id + book_guid in the request context.
func RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, `{"error":"missing authorization header"}`, http.StatusUnauthorized)
			return
		}

		tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
		if tokenStr == authHeader {
			http.Error(w, `{"error":"invalid authorization format"}`, http.StatusUnauthorized)
			return
		}

		claims, err := ParseToken(tokenStr)
		if err != nil {
			http.Error(w, `{"error":"invalid or expired token"}`, http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), UserIDKey, claims.UserID)
		ctx = context.WithValue(ctx, BookGUIDKey, claims.BookGUID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// ─── User Service ───────────────────────────────────────────────────────

type UserService struct {
	pool *pgxpool.Pool
}

func NewUserService(pool *pgxpool.Pool) *UserService {
	return &UserService{pool: pool}
}

type RegisterRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name"`
}

type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type AuthResponse struct {
	Token    string `json:"token"`
	UserID   string `json:"user_id"`
	BookGUID string `json:"book_guid"`
	Email    string `json:"email"`
	Name     string `json:"name"`
}

var (
	ErrEmailTaken   = errors.New("email already in use")
	ErrInvalidCreds = errors.New("invalid email or password")
)

// Register creates a new user, a book, seeds the default chart of accounts, and returns a JWT.
func (s *UserService) Register(ctx context.Context, req RegisterRequest) (*AuthResponse, error) {
	// Check email uniqueness
	var exists bool
	s.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM users WHERE email = $1)", req.Email).Scan(&exists)
	if exists {
		return nil, ErrEmailTaken
	}

	// Hash password
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	// Create user
	var userID string
	err = s.pool.QueryRow(ctx,
		"INSERT INTO users (email, password_hash, name) VALUES ($1, $2, $3) RETURNING id",
		req.Email, string(hash), req.Name,
	).Scan(&userID)
	if err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}

	// Create book for user
	var bookGUID string
	err = s.pool.QueryRow(ctx,
		"INSERT INTO books (user_id) VALUES ($1) RETURNING guid",
		userID,
	).Scan(&bookGUID)
	if err != nil {
		return nil, fmt.Errorf("create book: %w", err)
	}

	// Seed user book with default chart of accounts
	if err := seed.SeedUserBook(ctx, s.pool, bookGUID); err != nil {
		return nil, fmt.Errorf("seed user book: %w", err)
	}

	// Generate token
	token, err := GenerateToken(userID, bookGUID, req.Email)
	if err != nil {
		return nil, err
	}

	return &AuthResponse{
		Token:    token,
		UserID:   userID,
		BookGUID: bookGUID,
		Email:    req.Email,
		Name:     req.Name,
	}, nil
}

// Login authenticates a user and returns a JWT.
func (s *UserService) Login(ctx context.Context, req LoginRequest) (*AuthResponse, error) {
	var userID, hash, name string
	err := s.pool.QueryRow(ctx,
		"SELECT id, password_hash, name FROM users WHERE email = $1", req.Email,
	).Scan(&userID, &hash, &name)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrInvalidCreds
		}
		return nil, err
	}

	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)); err != nil {
		return nil, ErrInvalidCreds
	}

	// Find the user's book
	var bookGUID string
	err = s.pool.QueryRow(ctx,
		"SELECT guid FROM books WHERE user_id = $1 LIMIT 1", userID,
	).Scan(&bookGUID)
	if err != nil {
		return nil, fmt.Errorf("find book: %w", err)
	}

	token, err := GenerateToken(userID, bookGUID, req.Email)
	if err != nil {
		return nil, err
	}

	return &AuthResponse{
		Token:    token,
		UserID:   userID,
		BookGUID: bookGUID,
		Email:    req.Email,
		Name:     name,
	}, nil
}
