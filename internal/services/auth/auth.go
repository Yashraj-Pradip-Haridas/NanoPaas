package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/nanopaas/nanopaas/internal/domain"
)

var (
	ErrInvalidToken     = errors.New("invalid token")
	ErrExpiredToken     = errors.New("token expired")
	ErrInvalidClaims    = errors.New("invalid claims")
	ErrUserNotFound     = errors.New("user not found")
	ErrUnauthorized     = errors.New("unauthorized")
)

// Config holds auth configuration
type Config struct {
	JWTSecret        string
	JWTExpiry        time.Duration
	JWTRefreshExpiry time.Duration
}

// Claims represents JWT claims
type Claims struct {
	UserID    uuid.UUID       `json:"user_id"`
	Email     string          `json:"email"`
	Role      domain.UserRole `json:"role"`
	TokenType string          `json:"token_type"`
	jwt.RegisteredClaims
}

// TokenPair represents access and refresh tokens
type TokenPair struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	TokenType    string    `json:"token_type"`
}

// UserRepository interface for user persistence
type UserRepository interface {
	Create(ctx context.Context, user *domain.User) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.User, error)
	GetByEmail(ctx context.Context, email string) (*domain.User, error)
	GetByGitHubID(ctx context.Context, githubID int64) (*domain.User, error)
	Update(ctx context.Context, user *domain.User) error
	Delete(ctx context.Context, id uuid.UUID) error
}

// Service handles authentication
type Service struct {
	config   Config
	userRepo UserRepository
	logger   *zap.Logger
}

// NewService creates a new auth service
func NewService(config Config, userRepo UserRepository, logger *zap.Logger) *Service {
	return &Service{
		config:   config,
		userRepo: userRepo,
		logger:   logger,
	}
}

// GenerateTokens generates access and refresh tokens for a user
func (s *Service) GenerateTokens(user *domain.User) (*TokenPair, error) {
	now := time.Now()
	accessExpiry := now.Add(s.config.JWTExpiry)
	refreshExpiry := now.Add(s.config.JWTRefreshExpiry)

	// Access token
	accessClaims := &Claims{
		UserID:    user.ID,
		Email:     user.Email,
		Role:      user.Role,
		TokenType: "access",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(accessExpiry),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			Issuer:    "nanopaas",
			Subject:   user.ID.String(),
		},
	}

	accessToken := jwt.NewWithClaims(jwt.SigningMethodHS256, accessClaims)
	accessTokenString, err := accessToken.SignedString([]byte(s.config.JWTSecret))
	if err != nil {
		return nil, fmt.Errorf("failed to sign access token: %w", err)
	}

	// Refresh token
	refreshClaims := &Claims{
		UserID:    user.ID,
		Email:     user.Email,
		Role:      user.Role,
		TokenType: "refresh",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(refreshExpiry),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			Issuer:    "nanopaas",
			Subject:   user.ID.String(),
		},
	}

	refreshToken := jwt.NewWithClaims(jwt.SigningMethodHS256, refreshClaims)
	refreshTokenString, err := refreshToken.SignedString([]byte(s.config.JWTSecret))
	if err != nil {
		return nil, fmt.Errorf("failed to sign refresh token: %w", err)
	}

	s.logger.Debug("Generated tokens for user",
		zap.String("user_id", user.ID.String()),
		zap.Time("access_expires", accessExpiry),
	)

	return &TokenPair{
		AccessToken:  accessTokenString,
		RefreshToken: refreshTokenString,
		ExpiresAt:    accessExpiry,
		TokenType:    "Bearer",
	}, nil
}

// ValidateToken validates a JWT token and returns claims
func (s *Service) ValidateToken(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(s.config.JWTSecret), nil
	})

	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrExpiredToken
		}
		return nil, ErrInvalidToken
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, ErrInvalidClaims
	}

	return claims, nil
}

// RefreshTokens refreshes the token pair using a refresh token
func (s *Service) RefreshTokens(ctx context.Context, refreshToken string) (*TokenPair, error) {
	claims, err := s.ValidateToken(refreshToken)
	if err != nil {
		return nil, err
	}

	if claims.TokenType != "refresh" {
		return nil, ErrInvalidToken
	}

	user, err := s.userRepo.GetByID(ctx, claims.UserID)
	if err != nil {
		return nil, ErrUserNotFound
	}

	return s.GenerateTokens(user)
}

// GetUserFromToken retrieves user from a valid token
func (s *Service) GetUserFromToken(ctx context.Context, tokenString string) (*domain.User, error) {
	claims, err := s.ValidateToken(tokenString)
	if err != nil {
		return nil, err
	}

	user, err := s.userRepo.GetByID(ctx, claims.UserID)
	if err != nil {
		return nil, ErrUserNotFound
	}

	return user, nil
}

// AuthenticateGitHub handles GitHub OAuth authentication
func (s *Service) AuthenticateGitHub(ctx context.Context, githubID int64, login, email, name, avatarURL, token string) (*domain.User, *TokenPair, error) {
	// Check if user exists
	user, err := s.userRepo.GetByGitHubID(ctx, githubID)
	if err != nil {
		// Create new user
		user = domain.NewUserFromGitHub(githubID, login, email, name, avatarURL, token)
		if err := s.userRepo.Create(ctx, user); err != nil {
			return nil, nil, fmt.Errorf("failed to create user: %w", err)
		}
		s.logger.Info("New user created from GitHub",
			zap.String("user_id", user.ID.String()),
			zap.String("github_login", login),
		)
	} else {
		// Update existing user
		user.GitHubToken = token
		user.AvatarURL = avatarURL
		user.UpdateLastLogin()
		if err := s.userRepo.Update(ctx, user); err != nil {
			return nil, nil, fmt.Errorf("failed to update user: %w", err)
		}
		s.logger.Info("User logged in via GitHub",
			zap.String("user_id", user.ID.String()),
			zap.String("github_login", login),
		)
	}

	tokens, err := s.GenerateTokens(user)
	if err != nil {
		return nil, nil, err
	}

	return user, tokens, nil
}
