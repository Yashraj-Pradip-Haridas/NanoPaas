package domain

import (
	"time"

	"github.com/google/uuid"
)

// UserRole defines user roles
type UserRole string

const (
	UserRoleAdmin  UserRole = "admin"
	UserRoleMember UserRole = "member"
	UserRoleViewer UserRole = "viewer"
)

// User represents a platform user
type User struct {
	ID            uuid.UUID  `json:"id"`
	Email         string     `json:"email"`
	Name          string     `json:"name"`
	AvatarURL     string     `json:"avatar_url,omitempty"`
	GitHubID      int64      `json:"github_id,omitempty"`
	GitHubLogin   string     `json:"github_login,omitempty"`
	GitHubToken   string     `json:"-"` // Never expose in JSON
	Role          UserRole   `json:"role"`
	EmailVerified bool       `json:"email_verified"`
	LastLoginAt   *time.Time `json:"last_login_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// NewUser creates a new user
func NewUser(email, name string) *User {
	now := time.Now().UTC()
	return &User{
		ID:            uuid.New(),
		Email:         email,
		Name:          name,
		Role:          UserRoleMember,
		EmailVerified: false,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
}

// NewUserFromGitHub creates a user from GitHub OAuth
func NewUserFromGitHub(githubID int64, login, email, name, avatarURL, token string) *User {
	now := time.Now().UTC()
	return &User{
		ID:            uuid.New(),
		Email:         email,
		Name:          name,
		AvatarURL:     avatarURL,
		GitHubID:      githubID,
		GitHubLogin:   login,
		GitHubToken:   token,
		Role:          UserRoleMember,
		EmailVerified: true, // GitHub verified
		LastLoginAt:   &now,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
}

// UpdateGitHubToken updates the GitHub access token
func (u *User) UpdateGitHubToken(token string) {
	u.GitHubToken = token
	u.UpdatedAt = time.Now().UTC()
}

// UpdateLastLogin updates the last login timestamp
func (u *User) UpdateLastLogin() {
	now := time.Now().UTC()
	u.LastLoginAt = &now
	u.UpdatedAt = now
}

// IsAdmin checks if user is admin
func (u *User) IsAdmin() bool {
	return u.Role == UserRoleAdmin
}

// CanManageApp checks if user can manage an app
func (u *User) CanManageApp(app *App) bool {
	if u.IsAdmin() {
		return true
	}
	return app.OwnerID == u.ID
}

// Team represents a group of users
type Team struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Slug        string    `json:"slug"`
	Description string    `json:"description,omitempty"`
	OwnerID     uuid.UUID `json:"owner_id"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// NewTeam creates a new team
func NewTeam(name, slug string, ownerID uuid.UUID) *Team {
	now := time.Now().UTC()
	return &Team{
		ID:        uuid.New(),
		Name:      name,
		Slug:      slug,
		OwnerID:   ownerID,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// TeamMemberRole defines roles within a team
type TeamMemberRole string

const (
	TeamRoleOwner  TeamMemberRole = "owner"
	TeamRoleAdmin  TeamMemberRole = "admin"
	TeamRoleMember TeamMemberRole = "member"
)

// TeamMember represents a user's membership in a team
type TeamMember struct {
	ID        uuid.UUID      `json:"id"`
	TeamID    uuid.UUID      `json:"team_id"`
	UserID    uuid.UUID      `json:"user_id"`
	Role      TeamMemberRole `json:"role"`
	JoinedAt  time.Time      `json:"joined_at"`
	InvitedBy uuid.UUID      `json:"invited_by,omitempty"`
}

// NewTeamMember creates a new team membership
func NewTeamMember(teamID, userID uuid.UUID, role TeamMemberRole, invitedBy uuid.UUID) *TeamMember {
	return &TeamMember{
		ID:        uuid.New(),
		TeamID:    teamID,
		UserID:    userID,
		Role:      role,
		JoinedAt:  time.Now().UTC(),
		InvitedBy: invitedBy,
	}
}

// CanManageTeam checks if member can manage team settings
func (m *TeamMember) CanManageTeam() bool {
	return m.Role == TeamRoleOwner || m.Role == TeamRoleAdmin
}

// CanDeployApps checks if member can deploy apps
func (m *TeamMember) CanDeployApps() bool {
	return m.Role != ""
}
