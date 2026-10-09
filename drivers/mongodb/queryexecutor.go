package mongodb

import (
	"context"
	"fmt"
	"reflect"
	"regexp"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"orm"
)

// ---- orm.QueryExecutor ----

func (c *Conn) Select(ctx context.Context, q *orm.Query, dest any) error {
	filter, err := buildFilter(q.Wheres)
	if err != nil {
		return err
	}

	var raws []map[string]any

	if len(q.Joins) > 0 {
		pipeline, err := buildPipeline(q, filter)
		if err != nil {
			return err
		}
		cur, err := c.db.Collection(q.Table).Aggregate(ctx, pipeline)
		if err != nil {
			return err
		}
		defer cur.Close(ctx)
		if err := cur.All(ctx, &raws); err != nil {
			return err
		}
	} else {
		opts := options.Find()
		if q.Limit > 0 {
			opts.SetLimit(int64(q.Limit))
		}
		if q.Offset > 0 {
			opts.SetSkip(int64(q.Offset))
		}
		if len(q.OrderBy) > 0 {
			opts.SetSort(buildSort(q.OrderBy))
		}

		cur, err := c.db.Collection(q.Table).Find(ctx, filter, opts)
		if err != nil {
			return err
		}
		defer cur.Close(ctx)
		if err := cur.All(ctx, &raws); err != nil {
			return err
		}
	}

	return fillDest(dest, raws)
}

func (c *Conn) Insert(ctx context.Context, q *orm.Query) (orm.Result, error) {
	id, err := c.nextID(ctx, q.Table)
	if err != nil {
		return orm.Result{}, err
	}

	doc := bson.M{"id": id}
	for k, v := range q.Values {
		doc[k] = v
	}

	if _, err := c.db.Collection(q.Table).InsertOne(ctx, doc); err != nil {
		return orm.Result{}, err
	}
	return orm.Result{LastInsertID: id, RowsAffected: 1}, nil
}

func (c *Conn) Update(ctx context.Context, q *orm.Query) (orm.Result, error) {
	filter, err := buildFilter(q.Wheres)
	if err != nil {
		return orm.Result{}, err
	}

	set := bson.M{}
	for k, v := range q.Values {
		set[k] = v
	}

	res, err := c.db.Collection(q.Table).UpdateMany(ctx, filter, bson.M{"$set": set})
	if err != nil {
		return orm.Result{}, err
	}
	return orm.Result{RowsAffected: res.ModifiedCount}, nil
}

func (c *Conn) Delete(ctx context.Context, q *orm.Query) (orm.Result, error) {
	filter, err := buildFilter(q.Wheres)
	if err != nil {
		return orm.Result{}, err
	}

	res, err := c.db.Collection(q.Table).DeleteMany(ctx, filter)
	if err != nil {
		return orm.Result{}, err
	}
	return orm.Result{RowsAffected: res.DeletedCount}, nil
}

// nextID emulates an auto-increment int64 id (which Mongo does not have
// natively; it uses ObjectID) via a separate "counters" collection, the
// standard auto-increment pattern for MongoDB. We keep it so that
// the same model with `db:"id" int64` behaves the same on every driver.
func (c *Conn) nextID(ctx context.Context, table string) (int64, error) {
	var result struct {
		Seq int64 `bson:"seq"`
	}
	err := c.db.Collection("counters").FindOneAndUpdate(
		ctx,
		bson.M{"_id": table},
		bson.M{"$inc": bson.M{"seq": 1}},
		options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After),
	).Decode(&result)
	if err != nil {
		return 0, err
	}
	return result.Seq, nil
}

func fillDest(dest any, raws []map[string]any) error {
	v := reflect.ValueOf(dest)
	if v.Kind() != reflect.Ptr {
		return fmt.Errorf("mongodb: Select expects a pointer, got %T", dest)
	}
	if v.Elem().Kind() == reflect.Slice {
		return orm.FillSlice(dest, raws)
	}
	if len(raws) == 0 {
		return orm.ErrNotFound
	}
	return orm.FillStruct(dest, raws[0])
}

// ---- translating orm.Query into a bson filter/pipeline ----

// buildFilter translates orm.Condition into bson.M. Only homogeneous
// conditions are supported (see orm.HomogeneousConditions): mixing AND/OR
// in one Query is deliberately unsupported for NoSQL drivers (see the README).
func buildFilter(wheres []orm.Condition) (bson.M, error) {
	if len(wheres) == 0 {
		return bson.M{}, nil
	}
	if !orm.HomogeneousConditions(wheres) {
		return nil, fmt.Errorf("mongodb: mixing AND/OR in one Query is not supported; split it into several queries")
	}

	clauses := make([]bson.M, 0, len(wheres))
	for _, cond := range wheres {
		clause, err := buildCondition(cond)
		if err != nil {
			return nil, err
		}
		clauses = append(clauses, clause)
	}
	if len(clauses) == 1 {
		return clauses[0], nil
	}
	op := "$and"
	if wheres[1].Bool == "OR" {
		op = "$or"
	}
	return bson.M{op: clauses}, nil
}

func buildCondition(c orm.Condition) (bson.M, error) {
	switch strings.ToUpper(strings.TrimSpace(c.Op)) {
	case "=":
		return bson.M{c.Column: c.Value}, nil
	case "!=", "<>":
		return bson.M{c.Column: bson.M{"$ne": c.Value}}, nil
	case ">":
		return bson.M{c.Column: bson.M{"$gt": c.Value}}, nil
	case "<":
		return bson.M{c.Column: bson.M{"$lt": c.Value}}, nil
	case ">=":
		return bson.M{c.Column: bson.M{"$gte": c.Value}}, nil
	case "<=":
		return bson.M{c.Column: bson.M{"$lte": c.Value}}, nil
	case "IN":
		return bson.M{c.Column: bson.M{"$in": asList(c.Value)}}, nil
	case "NOT IN":
		return bson.M{c.Column: bson.M{"$nin": asList(c.Value)}}, nil
	case "LIKE":
		return bson.M{c.Column: bson.M{"$regex": likeToRegex(fmt.Sprint(c.Value)), "$options": "i"}}, nil
	default:
		return nil, fmt.Errorf("mongodb: operator %q is not supported", c.Op)
	}
}

// asList: IN with a scalar works as in SQL: col IN (v).
func asList(v any) any {
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array {
		return v
	}
	return []any{v}
}

// likeToRegex converts an SQL LIKE pattern (% and _) to a regex, escaping everything else.
func likeToRegex(pattern string) string {
	var b strings.Builder
	b.WriteString("^")
	for _, r := range pattern {
		switch r {
		case '%':
			b.WriteString(".*")
		case '_':
			b.WriteString(".")
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteString("$")
	return b.String()
}

func buildSort(orderBy []string) bson.D {
	var sort bson.D
	for _, o := range orderBy {
		parts := strings.Fields(o)
		if len(parts) == 0 {
			continue
		}
		dir := 1
		if len(parts) > 1 && strings.EqualFold(parts[1], "DESC") {
			dir = -1
		}
		sort = append(sort, bson.E{Key: parts[0], Value: dir})
	}
	return sort
}

// buildPipeline builds an aggregation pipeline for queries with JOINs.
// A JOIN is emulated via $lookup (Mongo's "SQL JOIN") + $unwind,
// which multiplies result rows the same way a regular SQL JOIN does:
// INNER JOIN is $unwind without preserving empty arrays (unmatched rows
// are dropped), LEFT JOIN uses preserveNullAndEmptyArrays: true.
//
// Limitation: in this version WHERE is applied BEFORE the JOIN (it filters only
// on fields of the base collection); filtering on fields of the joined table
// after $lookup is not supported yet.
func buildPipeline(q *orm.Query, filter bson.M) ([]bson.D, error) {
	var pipeline []bson.D

	if len(filter) > 0 {
		pipeline = append(pipeline, bson.D{{Key: "$match", Value: filter}})
	}

	for _, j := range q.Joins {
		localField, foreignField, err := parseJoinOn(j.On, q.Table, j.Table)
		if err != nil {
			return nil, err
		}

		pipeline = append(pipeline, bson.D{{Key: "$lookup", Value: bson.M{
			"from":         j.Table,
			"localField":   localField,
			"foreignField": foreignField,
			"as":           j.Table,
		}}})

		preserveNull := strings.EqualFold(j.Type, "LEFT")
		pipeline = append(pipeline, bson.D{{Key: "$unwind", Value: bson.M{
			"path":                       "$" + j.Table,
			"preserveNullAndEmptyArrays": preserveNull,
		}}})
	}

	if len(q.OrderBy) > 0 {
		pipeline = append(pipeline, bson.D{{Key: "$sort", Value: buildSort(q.OrderBy)}})
	}
	if q.Offset > 0 {
		pipeline = append(pipeline, bson.D{{Key: "$skip", Value: q.Offset}})
	}
	if q.Limit > 0 {
		pipeline = append(pipeline, bson.D{{Key: "$limit", Value: q.Limit}})
	}

	return pipeline, nil
}

// parseJoinOn parses an ON condition such as "orders.user_id = users.id" and
// determines localField/foreignField for $lookup based on which side
// belongs to the base table and which to the joined one.
func parseJoinOn(on, baseTable, joinTable string) (localField, foreignField string, err error) {
	sides := strings.SplitN(on, "=", 2)
	if len(sides) != 2 {
		return "", "", fmt.Errorf(`mongodb: cannot parse JOIN ON %q; expected the form "table.field = table.field"`, on)
	}

	leftTable, leftField, err := splitTableField(strings.TrimSpace(sides[0]))
	if err != nil {
		return "", "", err
	}
	rightTable, rightField, err := splitTableField(strings.TrimSpace(sides[1]))
	if err != nil {
		return "", "", err
	}

	switch {
	case leftTable == baseTable && rightTable == joinTable:
		return leftField, rightField, nil
	case rightTable == baseTable && leftTable == joinTable:
		return rightField, leftField, nil
	default:
		return "", "", fmt.Errorf("mongodb: JOIN ON %q must reference tables %q and %q", on, baseTable, joinTable)
	}
}

func splitTableField(s string) (table, field string, err error) {
	parts := strings.SplitN(s, ".", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("mongodb: expected the form table.field, got %q", s)
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), nil
}

var _ orm.QueryExecutor = (*Conn)(nil)
