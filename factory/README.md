# @orm/factory

Model factories for [`orm`](https://github.com/shibisty/orm.go) on
[`faker`](https://github.com/shibisty/faker.go), in the spirit of Laravel's: records for
tests and seeders in one line. Go package: `factory`. Kept apart from the core so that
`orm` does not depend on faker.

## Installation

```bash
gtr add orm @orm/factory
```

`orm` is a peer dependency (`^0.2`); `faker` comes with the factory.

## Usage

```go
import (
    "faker"
    "orm"
    factory "orm/factory"
)

var Users = factory.New(func(f *faker.Faker, i int) User {
    return User{
        Name:   f.Person.FullName(),
        Email:  f.Unique().Email(), // unique across every record of this factory
        Active: true,
    }
})

var Posts = factory.Fill[Post]() // from `fake:"..."` struct tags

u := Users.Make()                       // one record, not saved
list := Users.MakeMany(10)

admin, err := Users.State(func(u *User) { u.Role = "admin" }).Create(ctx, conn)
users, err := Users.Sequence(func(u *User, i int) { u.Role = []string{"admin", "editor"}[i%2] }).
    CreateMany(ctx, conn, 4)

// Related records: 3 posts for each of 5 users; 4 posts of one new user.
authors, err := factory.Has(Users, Posts, 3, func(u *User, p *Post) { p.UserID = u.ID }).
    CreateMany(ctx, conn, 5)
posts, err := factory.For(Posts, Users, func(p *Post, u *User) { p.UserID = u.ID }).
    CreateMany(ctx, conn, 4)

seeded := Users.Seed(42).MakeMany(3)    // the same records on every run
```

- A factory is immutable: `State`, `Sequence`, `Seed` and `AfterCreate` return a new one,
  so variants can be kept in variables (`Admins := Users.State(...)`).
- `Create` and `CreateMany` save through `orm.Repository[T]` on any driver: hooks,
  timestamps and ids work as for any `Create`. `AfterCreate(func(ctx, conn, *T) error)`
  runs after each save.
- On an error the records saved so far stay saved; create inside a transaction to undo
  them (`orm.AsQueryExecutor(tx)`).
- `i` in the definition and in `Sequence` is the record's index within one call.
- `Faker()` gives the generator, to set `Locale` (`"ru"`, `"ua"`) or `RefDate`.

## Tests

In a checkout, run `gtr install` once, then:

```bash
gtr run test -- -race -cover   # 100% coverage, no database needed
```

## License

MIT
