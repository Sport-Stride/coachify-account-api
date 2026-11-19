package services

import (
	"context"
	"time"

	"coachify-account-api/models/db"
	"coachify-account-api/utils"
)

// registrationLinkSource streams the legacy registration links.
type registrationLinkSource interface {
	ForEach(ctx context.Context, fn func(db.RegistrationLink) error) error
}

// inviteBackfillStore is the part of the invite link repository the backfill needs.
type inviteBackfillStore interface {
	GetByHash(ctx context.Context, hash string) (*db.InviteLink, error)
	UpsertByHash(ctx context.Context, l *db.InviteLink) (bool, error)
}

// InviteBackfillReport counts what a backfill run saw and did.
type InviteBackfillReport struct {
	Legacy      int
	Existing    int
	WouldCreate int
	Created     int
	Failed      int
}

// RunInviteBackfill copies registration_links into hashed invite_links rows (kind client_personal, unlimited uses,
// no expiry) so legacy links keep resolving through the hashed path. The legacy collection and its raw tokens are
// never touched. It is idempotent (a link whose hash already exists is skipped) and a dry run only reports.
func RunInviteBackfill(ctx context.Context, src registrationLinkSource, dst inviteBackfillStore, dryRun bool, logf func(string, ...any)) (InviteBackfillReport, error) {
	var rep InviteBackfillReport
	err := src.ForEach(ctx, func(l db.RegistrationLink) error {
		rep.Legacy++
		if l.Token == "" || l.CoachID == "" {
			rep.Failed++
			logf("skip registration link %s: missing token or coach", l.ID.Hex())
			return nil
		}
		hash := utils.HashToken(l.Token)
		cur, err := dst.GetByHash(ctx, hash)
		if err != nil {
			return err
		}
		if cur != nil {
			rep.Existing++
			return nil
		}
		if dryRun {
			rep.WouldCreate++
			return nil
		}
		created := l.CreatedAt
		if created.IsZero() {
			created = time.Now().UTC()
		}
		inserted, err := dst.UpsertByHash(ctx, &db.InviteLink{
			Kind: db.InviteKindClientPersonal, TokenHash: hash, CreatorID: l.CoachID, CreatorRole: "legacy",
			Status: db.InviteStatusActive, CreatedAt: created, LegacyRef: "registration_links:" + l.ID.Hex(),
		})
		if err != nil {
			rep.Failed++
			logf("backfill registration link %s failed: %v", l.ID.Hex(), err)
			return nil
		}
		if inserted {
			rep.Created++
		} else {
			rep.Existing++
		}
		return nil
	})
	return rep, err
}
