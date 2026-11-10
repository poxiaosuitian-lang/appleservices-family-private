package appleservices

import (
	"context"
	"github.com/Laky-64/appleservices/icloud"
)

type protectedStorePersistence interface {
	LoadStoreSession(string, string) ([]byte, error)
	SaveStoreSession(string, string, []byte) error
}

// RestoreStorePurchaseSession uses an OS-protected, account-bound checkpoint
// only after Apple accepts a read-only subscription request. No purchase or
// redemption is replayed, and a restored file alone never means Store-ready.
func (l *Login) RestoreStorePurchaseSession(ctx context.Context) (bool, error) {
	store, ok := l.store.(protectedStorePersistence)
	if !ok || l.needsTwoFactor || l.client == nil {
		return false, nil
	}
	if l.StorePurchaseSessionReady() {
		return true, nil
	}
	data, err := store.LoadStoreSession(l.creds.AppleID, l.client.DSID)
	if err != nil {
		return false, nil
	}
	defer clear(data)
	session, err := icloud.RestoreFrameworkStore(data, l.creds.AppleID, l.client.DSID)
	if err != nil {
		return false, nil
	}
	if _, err := session.ReadSkillSubscription(ctx); err != nil {
		if !session.MatchesAccount(l.client.DSID) {
			return false, nil
		}
		return false, err
	}
	l.storeCommerce = session
	return true, nil
}

func (l *Login) saveStorePurchaseSession() error {
	store, ok := l.store.(protectedStorePersistence)
	if !ok {
		return nil
	}
	data, err := l.storeCommerce.PrivateCheckpoint(l.creds.AppleID, l.client.DSID)
	if err != nil {
		return err
	}
	defer clear(data)
	return store.SaveStoreSession(l.creds.AppleID, l.client.DSID, data)
}
