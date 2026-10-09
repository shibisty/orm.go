package main

import (
	"context"
	"fmt"
	"time"

	"faker"
	"orm"
	"orm/schema"
)

// User is a single struct that describes both storage (db tags) and fake
// data generation for the seeder (fake tags).
type User struct {
	orm.BaseModel
	Name      string    `db:"name"       fake:"full_name"`
	Email     string    `db:"email"      fake:"-"` // unique, filled in separately
	Age       int       `db:"age"        fake:"int:18,65"`
	City      string    `db:"city"       fake:"city"`
	Bio       string    `db:"bio"        fake:"paragraph"`
	CreatedAt time.Time `db:"created_at" fake:"date_past:2"`
}

func (User) TableName() string { return "seed_users" }

// CreateTable recreates the seed_users table.
func CreateTable(ctx context.Context, exec orm.SQLExecutor) error {
	sb, err := schema.NewFor(exec)
	if err != nil {
		return err
	}
	if err := sb.DropIfExists(ctx, "seed_users"); err != nil {
		return err
	}
	return sb.Create(ctx, "seed_users", func(t *schema.Blueprint) {
		t.ID()
		t.String("name")
		t.String("email").Unique()
		t.Integer("age")
		t.String("city")
		t.Text("bio")
		t.Timestamp("created_at")
	})
}

// RefDate is the reference point for seeder dates (faker.RefDate): together
// with the seed it makes the data fully reproducible.
var RefDate = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// SeedUsers creates n users. The same seed always yields the same data, so
// the seed is reproducible in CI. It works on top of any orm driver.
func SeedUsers(ctx context.Context, exec orm.QueryExecutor, seed int64, n int) ([]User, error) {
	f := faker.New(seed)
	f.RefDate = RefDate
	unique := f.Unique()
	repo := orm.NewRepository[User](exec)
	users := make([]User, 0, n)
	for i := 0; i < n; i++ {
		var u User
		if err := f.FillStruct(&u); err != nil {
			return nil, err
		}
		u.Email = unique.Email() // UNIQUE in the table, so no duplicates
		u.CreatedAt = u.CreatedAt.UTC().Truncate(time.Microsecond)
		if err := repo.Create(ctx, &u); err != nil {
			return nil, fmt.Errorf("user %d: %w", i+1, err)
		}
		users = append(users, u)
	}
	return users, nil
}
