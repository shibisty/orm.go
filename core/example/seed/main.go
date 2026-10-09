// Seeder built on orm + faker: fills a table with reproducible fake data.
//
//	go run . -dsn "postgres://user:pass@localhost:5432/app?sslmode=disable" -n 100
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"orm"
	postgres "orm-postgres"
)

func main() {
	dsn := flag.String("dsn", os.Getenv("DATABASE_DSN"), "PostgreSQL connection string (or DATABASE_DSN)")
	n := flag.Int("n", 20, "number of users to create")
	seed := flag.Int64("seed", 42, "generator seed: the same seed yields the same data")
	flag.Parse()
	ctx := context.Background()

	conn, err := orm.New(ctx, postgres.Driver(*dsn))
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()
	sqlConn, ok := conn.(orm.SQLExecutor)
	if !ok {
		log.Fatalf("driver %s is not SQL", conn.Driver())
	}
	if err := CreateTable(ctx, sqlConn); err != nil {
		log.Fatal(err)
	}
	users, err := SeedUsers(ctx, sqlConn.(orm.QueryExecutor), *seed, *n)
	if err != nil {
		log.Fatal(err)
	}
	for _, u := range users {
		fmt.Printf("%4d  %-24s %-32s %2d  %s\n", u.ID, u.Name, u.Email, u.Age, u.City)
	}
}
