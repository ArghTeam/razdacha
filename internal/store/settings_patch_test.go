package store

import (
	"context"
	"reflect"
	"testing"
)

func TestUpdateSettingsSerializesFreshPartialMerges(t *testing.T) {
	st := openAt(t, t.TempDir()+"/state.db")
	ctx := context.Background()
	entered, release, started := make(chan struct{}), make(chan struct{}), make(chan struct{})
	first, second := make(chan error, 1), make(chan error, 1)
	go func() {
		_, _, err := st.UpdateSettings(ctx, func(v Settings) Settings {
			close(entered)
			<-release
			v.DNSUpstream = "9.9.9.9"
			return v
		})
		first <- err
	}()
	<-entered
	var saved Settings
	go func() {
		close(started)
		_, after, err := st.UpdateSettings(ctx, func(v Settings) Settings {
			if v.DNSUpstream != "9.9.9.9" {
				t.Error("second merge read stale settings")
			}
			v.LogLevel = "error"
			return v
		})
		saved = after
		second <- err
	}()
	<-started
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if err := <-second; err != nil {
		t.Fatal(err)
	}
	current, err := st.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if current.DNSUpstream != "9.9.9.9" || current.LogLevel != "error" || !reflect.DeepEqual(current, saved) {
		t.Fatal("transactional partial merge lost concurrent fields or returned stale state")
	}
}
