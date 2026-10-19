package repository

import "context"

type ItemFilter struct {
	Query       string
	Years       []int
	TagIDs      []string
	StudioIDs   []string
	PersonIDs   []string
	IsFavorite  *bool
	IsWatched   *bool
	HasPlayable *bool
	Limit       int
	Cursor      string
}

type ItemRecord struct {
	ID              string
	Source          string
	Code            string
	RawCode         string
	Title           string
	TranslatedTitle string
	OriginalTitle   string
	Overview        string
	Year            *int
	MetadataStatus  string
	IsAdult         bool
}

type ItemRepository interface {
	GetByID(ctx context.Context, itemID string) (ItemRecord, error)
	List(ctx context.Context, filter ItemFilter) ([]ItemRecord, string, error)
}

type UserRecord struct {
	ID          string
	Username    string
	DisplayName string
	Status      string
	IsAdmin     bool
}

type UserRepository interface {
	GetByID(ctx context.Context, userID string) (UserRecord, error)
	Create(ctx context.Context, user UserRecord) error
}
