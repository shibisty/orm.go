// orm example: one model and one Repository on top of any driver.
//
//	go run . -driver postgres -dsn "postgres://user:pass@localhost:5432/app?sslmode=disable"
//	go run . -driver mysql    -dsn "root:@tcp(localhost:3306)/app"
//	go run . -driver redis    -dsn "redis://localhost:6379/0"
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"orm"
	mysql "orm-mysql"
	postgres "orm-postgres"
	redis "orm-redis"
	"orm/schema"
)

// User is a plain model, as in Eloquent: just `db` tags and TableName().
// The same struct works on SQL, Redis and MongoDB.
type User struct {
	orm.BaseModel        // ID field tagged db:"id"
	Name          string `db:"name"`
	Email         string `db:"email"`
}

func (User) TableName() string { return "example_users" }

func main() {
	driver := flag.String("driver", "postgres", "mysql | postgres | redis")
	dsn := flag.String("dsn", os.Getenv("DATABASE_DSN"), "connection string (or DATABASE_DSN)")
	flag.Parse()
	ctx := context.Background()

	var d orm.Driver
	switch *driver {
	case "mysql":
		d = mysql.Driver(*dsn)
	case "postgres":
		d = postgres.Driver(*dsn)
	case "redis":
		d = redis.Driver(*dsn)
	default:
		log.Fatalf("unknown driver %q", *driver)
	}

	// The driver is passed explicitly, with no string names and no global registry.
	conn, err := orm.New(ctx, d)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	// SQL databases need a table; Redis and MongoDB work without a schema.
	if exec, ok := conn.(orm.SQLExecutor); ok {
		sb, err := schema.NewFor(exec)
		if err != nil {
			log.Fatal(err)
		}
		if err := sb.DropIfExists(ctx, "example_users"); err != nil {
			log.Fatal(err)
		}
		err = sb.Create(ctx, "example_users", func(t *schema.Blueprint) {
			t.ID()
			t.String("name")
			t.String("email").Unique()
		})
		if err != nil {
			log.Fatal(err)
		}
	}

	users := orm.NewRepository[User](conn.(orm.QueryExecutor))

	for _, u := range []*User{{Name: "Ivan", Email: "ivan@example.com"}, {Name: "Olena", Email: "olena@example.com"}} {
		if err := users.Create(ctx, u); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("created %s, id=%d\n", u.Name, u.ID)
	}

	found, err := users.Find(ctx, 1)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("found: %+v\n", *found)

	list, err := users.All(ctx, orm.NewQuery("example_users").
		Where("email", "LIKE", "%@example.com").
		OrderByDesc("id").
		LimitOffset(10, 0))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("total found:", len(list))
}
