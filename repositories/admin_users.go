package repositories

import (
	"context"
	"regexp"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"

	"coachify-account-api/models/db"
)

const (
	maxAdminSearchLen = 64
	// statsTimezone buckets signups per local day. ASSUMPTION: the business operates in Tunisia.
	statsTimezone = "Africa/Tunis"
	statsDateFmt  = "%Y-%m-%d"
)

// userSearchClause builds the case-insensitive literal $or over firstname/lastname/email.
// q is trimmed and capped at 64 characters; it returns false when q is empty.
func userSearchClause(q string) (bson.E, bool) {
	q = strings.TrimSpace(q)
	if r := []rune(q); len(r) > maxAdminSearchLen {
		q = string(r[:maxAdminSearchLen])
	}
	if q == "" {
		return bson.E{}, false
	}
	rx := bson.D{{Key: "$regex", Value: regexp.QuoteMeta(q)}, {Key: "$options", Value: "i"}}
	return bson.E{Key: "$or", Value: bson.A{
		bson.D{{Key: "firstname", Value: rx}},
		bson.D{{Key: "lastname", Value: rx}},
		bson.D{{Key: "email", Value: rx}},
	}}, true
}

// adminUserSort maps the API sort key and order to a Mongo sort (externalid is the stable tiebreak).
func adminUserSort(sortKey, order string) bson.D {
	dir := 1
	if order == db.AdminOrderDesc {
		dir = -1
	}
	var s bson.D
	switch sortKey {
	case db.AdminUserSortName:
		s = bson.D{{Key: "lastname", Value: dir}, {Key: "firstname", Value: dir}}
	case db.AdminUserSortEmail:
		s = bson.D{{Key: "email", Value: dir}}
	case db.AdminUserSortLastLogin:
		s = bson.D{{Key: "last_login", Value: dir}}
	default:
		s = bson.D{{Key: "created_at", Value: dir}}
	}
	return append(s, bson.E{Key: "externalid", Value: 1})
}

func firstOr(path string, def interface{}) bson.D {
	return bson.D{{Key: "$ifNull", Value: bson.A{bson.D{{Key: "$first", Value: path}}, def}}}
}

// ListAdminUsers returns one page of admin user rows and the total match count, in one aggregation.
func (r *UserRepository) ListAdminUsers(ctx context.Context, q db.AdminUserQuery) ([]db.AdminUserRow, int, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	page, size := q.Page, q.Size
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 20
	}

	match := bson.D{}
	if q.Role != "" {
		match = append(match, bson.E{Key: "role", Value: q.Role})
	}
	if q.Status != "" {
		match = append(match, bson.E{Key: "status", Value: q.Status})
	}
	if len(q.IDs) > 0 {
		match = append(match, bson.E{Key: "externalid", Value: bson.D{{Key: "$in", Value: q.IDs}}})
	}
	if clause, ok := userSearchClause(q.Q); ok {
		match = append(match, clause)
	}

	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: match}},
		{{Key: "$facet", Value: bson.D{
			{Key: "items", Value: bson.A{
				bson.D{{Key: "$sort", Value: adminUserSort(q.Sort, q.Order)}},
				bson.D{{Key: "$skip", Value: int64((page - 1) * size)}},
				bson.D{{Key: "$limit", Value: int64(size)}},
				bson.D{{Key: "$project", Value: adminUserRowProjection()}},
			}},
			{Key: "total", Value: bson.A{bson.D{{Key: "$count", Value: "n"}}}},
		}}},
	}

	cursor, err := r.collection.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, 0, err
	}
	defer cursor.Close(ctx)

	var out []struct {
		Items []db.AdminUserRow `bson:"items"`
		Total []struct {
			N int `bson:"n"`
		} `bson:"total"`
	}
	if err := cursor.All(ctx, &out); err != nil {
		return nil, 0, err
	}
	if len(out) == 0 {
		return []db.AdminUserRow{}, 0, nil
	}
	total := 0
	if len(out[0].Total) > 0 {
		total = out[0].Total[0].N
	}
	if out[0].Items == nil {
		out[0].Items = []db.AdminUserRow{}
	}
	return out[0].Items, total, nil
}

// adminUserRowProjection is a whitelist: password, tokens and codes are never selected.
func adminUserRowProjection() bson.D {
	return bson.D{
		{Key: "_id", Value: 0},
		{Key: "externalid", Value: 1},
		{Key: "firstname", Value: 1},
		{Key: "lastname", Value: 1},
		{Key: "email", Value: 1},
		{Key: "role", Value: 1},
		{Key: "status", Value: 1},
		{Key: "profile_picture", Value: 1},
		{Key: "verification_status", Value: bson.D{{Key: "$eq", Value: bson.A{"$verification_status", true}}}},
		{Key: "matricule_fiscale_status", Value: bson.D{{Key: "$ifNull", Value: bson.A{"$matricule_fiscale_status", string(db.MatriculeFiscaleNone)}}}},
		{Key: "is_head_coach", Value: bson.D{{Key: "$eq", Value: bson.A{"$is_head_coach", true}}}},
		{Key: "created_at", Value: 1},
		{Key: "last_login", Value: 1},
	}
}

// GetAdminUserDetail returns one user with coach / head coach / clients count in a single aggregation.
// It returns (nil, nil) when the user does not exist.
func (r *UserRepository) GetAdminUserDetail(ctx context.Context, externalID string) (*db.AdminUserDetail, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	idMatch := func(field string, v interface{}) bson.D {
		return bson.D{{Key: "$match", Value: bson.D{{Key: "$expr", Value: bson.D{{Key: "$eq", Value: bson.A{field, v}}}}}}}
	}
	namesProject := bson.D{{Key: "$project", Value: bson.D{
		{Key: "_id", Value: 0}, {Key: "externalid", Value: 1}, {Key: "firstname", Value: 1}, {Key: "lastname", Value: 1},
	}}}

	proj := adminUserRowProjection()
	proj = append(proj,
		bson.E{Key: "phone_number", Value: 1},
		bson.E{Key: "gender", Value: 1},
		bson.E{Key: "address", Value: 1},
		bson.E{Key: "metadata", Value: 1},
		bson.E{Key: "updated_at", Value: 1},
		bson.E{Key: "head_coach_id", Value: firstOr("$m.head_coach_id", "")},
		bson.E{Key: "head_coach_name", Value: bson.D{{Key: "$trim", Value: bson.D{{Key: "input", Value: bson.D{{Key: "$concat", Value: bson.A{
			firstOr("$hc.firstname", ""), " ", firstOr("$hc.lastname", ""),
		}}}}}}}},
		bson.E{Key: "coach", Value: bson.D{{Key: "$cond", Value: bson.A{
			bson.D{{Key: "$gt", Value: bson.A{bson.D{{Key: "$size", Value: "$cu"}}, 0}}},
			bson.D{
				{Key: "external_id", Value: bson.D{{Key: "$first", Value: "$cu.externalid"}}},
				{Key: "firstname", Value: bson.D{{Key: "$first", Value: "$cu.firstname"}}},
				{Key: "lastname", Value: bson.D{{Key: "$first", Value: "$cu.lastname"}}},
			},
			nil,
		}}}},
		bson.E{Key: "clients_count", Value: bson.D{{Key: "$cond", Value: bson.A{
			bson.D{{Key: "$eq", Value: bson.A{"$role", "coach"}}},
			firstOr("$cc.n", 0),
			"$$REMOVE",
		}}}},
	)

	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: bson.D{{Key: "externalid", Value: externalID}}}},
		{{Key: "$limit", Value: 1}},
		// the coach of this user (client side of coach_clients), then the coach's name
		{{Key: "$lookup", Value: bson.D{
			{Key: "from", Value: "coach_clients"},
			{Key: "let", Value: bson.D{{Key: "id", Value: "$externalid"}}},
			{Key: "pipeline", Value: bson.A{
				idMatch("$client_id", "$$id"),
				bson.D{{Key: "$limit", Value: 1}},
				bson.D{{Key: "$lookup", Value: bson.D{
					{Key: "from", Value: r.collection.Name()},
					{Key: "let", Value: bson.D{{Key: "cid", Value: "$coach_id"}}},
					{Key: "pipeline", Value: bson.A{idMatch("$externalid", "$$cid"), namesProject}},
					{Key: "as", Value: "u"},
				}}},
				bson.D{{Key: "$unwind", Value: "$u"}},
				bson.D{{Key: "$replaceRoot", Value: bson.D{{Key: "newRoot", Value: "$u"}}}},
			}},
			{Key: "as", Value: "cu"},
		}}},
		// clients count when this user is a coach
		{{Key: "$lookup", Value: bson.D{
			{Key: "from", Value: "coach_clients"},
			{Key: "let", Value: bson.D{{Key: "id", Value: "$externalid"}}},
			{Key: "pipeline", Value: bson.A{idMatch("$coach_id", "$$id"), bson.D{{Key: "$count", Value: "n"}}}},
			{Key: "as", Value: "cc"},
		}}},
		// head coach membership, then the head coach's name
		{{Key: "$lookup", Value: bson.D{
			{Key: "from", Value: "coach_team_members"},
			{Key: "localField", Value: "externalid"},
			{Key: "foreignField", Value: "coach_id"},
			{Key: "as", Value: "m"},
		}}},
		{{Key: "$lookup", Value: bson.D{
			{Key: "from", Value: r.collection.Name()},
			{Key: "let", Value: bson.D{{Key: "hid", Value: firstOr("$m.head_coach_id", "")}}},
			{Key: "pipeline", Value: bson.A{idMatch("$externalid", "$$hid"), namesProject}},
			{Key: "as", Value: "hc"},
		}}},
		{{Key: "$project", Value: proj}},
	}

	cursor, err := r.collection.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var out []db.AdminUserDetail
	if err := cursor.All(ctx, &out); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, nil
	}
	return &out[0], nil
}

// AdminUserStats summarizes the user base in one $facet aggregation. now anchors the relative windows.
func (r *UserRepository) AdminUserStats(ctx context.Context, days int, now time.Time) (*db.AdminUserStats, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	countWhere := func(m bson.D) bson.A {
		return bson.A{bson.D{{Key: "$match", Value: m}}, bson.D{{Key: "$count", Value: "n"}}}
	}
	groupBy := func(field string) bson.A {
		return bson.A{bson.D{{Key: "$group", Value: bson.D{{Key: "_id", Value: field}, {Key: "n", Value: bson.D{{Key: "$sum", Value: 1}}}}}}}
	}
	since := func(d int) bson.D {
		return bson.D{{Key: "created_at", Value: bson.D{{Key: "$gte", Value: now.AddDate(0, 0, -d)}}}}
	}

	pipeline := mongo.Pipeline{
		{{Key: "$project", Value: bson.D{
			{Key: "_id", Value: 0}, {Key: "role", Value: 1}, {Key: "status", Value: 1},
			{Key: "created_at", Value: 1}, {Key: "matricule_fiscale_status", Value: 1},
		}}},
		{{Key: "$facet", Value: bson.D{
			{Key: "by_role", Value: groupBy("$role")},
			{Key: "by_status", Value: groupBy("$status")},
			{Key: "new_7d", Value: countWhere(since(7))},
			{Key: "new_30d", Value: countWhere(since(30))},
			{Key: "signups", Value: bson.A{
				bson.D{{Key: "$match", Value: since(days)}},
				bson.D{{Key: "$group", Value: bson.D{
					{Key: "_id", Value: bson.D{
						{Key: "date", Value: bson.D{{Key: "$dateToString", Value: bson.D{
							{Key: "format", Value: statsDateFmt},
							{Key: "date", Value: "$created_at"},
							{Key: "timezone", Value: statsTimezone},
						}}}},
						{Key: "role", Value: "$role"},
					}},
					{Key: "n", Value: bson.D{{Key: "$sum", Value: 1}}},
				}}},
				bson.D{{Key: "$sort", Value: bson.D{{Key: "_id.date", Value: 1}, {Key: "_id.role", Value: 1}}}},
			}},
			{Key: "to_confirm", Value: countWhere(bson.D{{Key: "status", Value: string(db.ToConfirm)}})},
			{Key: "pending_matricule", Value: countWhere(bson.D{{Key: "matricule_fiscale_status", Value: string(db.MatriculeFiscalePending)}})},
		}}},
	}

	cursor, err := r.collection.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	type countDoc struct {
		N int `bson:"n"`
	}
	type groupDoc struct {
		ID string `bson:"_id"`
		N  int    `bson:"n"`
	}
	var out []struct {
		ByRole   []groupDoc `bson:"by_role"`
		ByStatus []groupDoc `bson:"by_status"`
		New7d    []countDoc `bson:"new_7d"`
		New30d   []countDoc `bson:"new_30d"`
		Signups  []struct {
			ID struct {
				Date string `bson:"date"`
				Role string `bson:"role"`
			} `bson:"_id"`
			N int `bson:"n"`
		} `bson:"signups"`
		ToConfirm        []countDoc `bson:"to_confirm"`
		PendingMatricule []countDoc `bson:"pending_matricule"`
	}
	if err := cursor.All(ctx, &out); err != nil {
		return nil, err
	}

	res := &db.AdminUserStats{
		ByRole:      map[string]int{},
		ByStatus:    map[string]int{},
		Signups:     []db.AdminSignupPoint{},
		GeneratedAt: now,
	}
	if len(out) == 0 {
		return res, nil
	}
	n := func(c []countDoc) int {
		if len(c) == 0 {
			return 0
		}
		return c[0].N
	}
	o := out[0]
	for _, g := range o.ByRole {
		res.ByRole[g.ID] = g.N
	}
	for _, g := range o.ByStatus {
		res.ByStatus[g.ID] = g.N
	}
	for _, s := range o.Signups {
		res.Signups = append(res.Signups, db.AdminSignupPoint{Date: s.ID.Date, Role: s.ID.Role, Count: s.N})
	}
	res.NewUsers = db.AdminNewUsers{Last7d: n(o.New7d), Last30d: n(o.New30d)}
	res.ToConfirm = n(o.ToConfirm)
	res.PendingMatricule = n(o.PendingMatricule)
	return res, nil
}

// statusUpdateDoc builds the single update for a status change. A non-Active target also loses its
// token and refresh_token, which forces a re-login.
func statusUpdateDoc(status string, now time.Time) bson.D {
	u := bson.D{{Key: "$set", Value: bson.D{
		{Key: "status", Value: status},
		{Key: "updated_at", Value: now},
	}}}
	if status != string(db.Active) {
		u = append(u, bson.E{Key: "$unset", Value: bson.D{
			{Key: "token", Value: ""},
			{Key: "refresh_token", Value: ""},
		}})
	}
	return u
}

// UpdateStatusAndRevoke sets the user status in one UpdateOne and, for non-Active targets, clears the
// stored tokens. It returns false when the user does not exist.
func (r *UserRepository) UpdateStatusAndRevoke(ctx context.Context, externalID, status string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	res, err := r.collection.UpdateOne(ctx, bson.D{{Key: "externalid", Value: externalID}}, statusUpdateDoc(status, time.Now()))
	if err != nil {
		return false, err
	}
	return res.MatchedCount > 0, nil
}
