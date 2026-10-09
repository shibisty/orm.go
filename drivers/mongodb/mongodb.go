// Package mongodb implements orm.DocumentStore (NOT orm.SQLExecutor:
// Mongo has no JOIN/SQL, so there is no point forcing it into an SQL interface).
//
// It depends on go.mongodb.org/mongo-driver/v2, which gtr installs with it.
package mongodb

import (
	"context"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"orm"
)

// mongoDriver is the orm.Driver implementation for MongoDB.
type mongoDriver struct {
	dsn    string
	dbName []string
}

// Driver returns an orm.Driver for explicit injection:
//
//	conn, err := orm.New(ctx, mongodb.Driver("mongodb://localhost:27017", "mydb"))
//
// The connection is created in Open; mongo-driver v2 connects lazily, so
// network errors surface on the first query or Ping.
func Driver(dsn string, dbName ...string) orm.Driver { return mongoDriver{dsn: dsn, dbName: dbName} }

func (d mongoDriver) Open(ctx context.Context) (orm.Connection, error) {
	return Open(ctx, d.dsn, d.dbName...)
}

// Conn implements orm.DocumentStore.
type Conn struct {
	client *mongo.Client
	db     *mongo.Database
}

// Open connects to MongoDB. dsn is, for example, "mongodb://localhost:27017".
// The database name is the second argument; without it, "default" is used.
func Open(ctx context.Context, dsn string, dbName ...string) (*Conn, error) {
	client, err := mongo.Connect(options.Client().ApplyURI(dsn))
	if err != nil {
		return nil, err
	}
	name := "default"
	if len(dbName) > 0 {
		name = dbName[0]
	}
	return &Conn{client: client, db: client.Database(name)}, nil
}

func (c *Conn) Driver() string { return "mongodb" }

func (c *Conn) Ping(ctx context.Context) error {
	return c.client.Ping(ctx, nil)
}

func (c *Conn) Close() error {
	return c.client.Disconnect(context.Background())
}

func (c *Conn) InsertOne(ctx context.Context, collection string, document any) (any, error) {
	res, err := c.db.Collection(collection).InsertOne(ctx, document)
	if err != nil {
		return nil, err
	}
	return res.InsertedID, nil
}

func (c *Conn) FindOne(ctx context.Context, collection string, filter orm.Filter, dest any) error {
	return c.db.Collection(collection).FindOne(ctx, bson.M(filter)).Decode(dest)
}

func (c *Conn) Find(ctx context.Context, collection string, filter orm.Filter, dest any) error {
	cur, err := c.db.Collection(collection).Find(ctx, bson.M(filter))
	if err != nil {
		return err
	}
	defer cur.Close(ctx)
	return cur.All(ctx, dest)
}

func (c *Conn) UpdateOne(ctx context.Context, collection string, filter orm.Filter, update any) (int64, int64, error) {
	res, err := c.db.Collection(collection).UpdateOne(ctx, bson.M(filter), bson.M{"$set": update})
	if err != nil {
		return 0, 0, err
	}
	return res.MatchedCount, res.ModifiedCount, nil
}

func (c *Conn) DeleteOne(ctx context.Context, collection string, filter orm.Filter) (int64, error) {
	res, err := c.db.Collection(collection).DeleteOne(ctx, bson.M(filter))
	if err != nil {
		return 0, err
	}
	return res.DeletedCount, nil
}

// Compile-time check that Conn implements orm.DocumentStore.
var _ orm.DocumentStore = (*Conn)(nil)
