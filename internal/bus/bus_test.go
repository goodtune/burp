package bus

import (
	"testing"
	"time"
)

func TestTopics(t *testing.T) {
	if PRTopic("acme", "api", 42) != "pr:acme/api#42" {
		t.Fatal(PRTopic("acme", "api", 42))
	}
	if UserTopic("octocat") != "user:octocat" || SHATopic("abc") != "sha:abc" {
		t.Fatal("topic builders")
	}
	if itoa(0) != "0" || itoa(-12) != "-12" || itoa(1234567) != "1234567" {
		t.Fatal("itoa")
	}
}

func TestPublishSubscribe(t *testing.T) {
	b := New()
	ch, cancel := b.Subscribe("a", "b")
	defer cancel()
	other, cancelOther := b.Subscribe("c")
	defer cancelOther()

	b.Publish(Event{Topic: "a", Kind: "pull_request"})
	b.Publish(Event{Topic: "z"})
	select {
	case ev := <-ch:
		if ev.Topic != "a" || ev.Kind != "pull_request" || ev.At.IsZero() {
			t.Fatalf("unexpected event %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("no event")
	}
	select {
	case ev := <-other:
		t.Fatalf("unexpected event on other topic: %+v", ev)
	default:
	}
	cancel()
	cancel() // idempotent
	b.Publish(Event{Topic: "a"})
	select {
	case ev, ok := <-ch:
		if ok {
			t.Fatalf("received after cancel: %+v", ev)
		}
	default:
	}
	subs, pub, _ := b.Stats()
	if subs != 1 || pub != 3 {
		t.Fatalf("stats %d %d", subs, pub)
	}
}

func TestSetTopics(t *testing.T) {
	b := New()
	sub := b.Open("a")
	defer sub.Cancel()
	sub.SetTopics("b")
	b.Publish(Event{Topic: "a"})
	b.Publish(Event{Topic: "b"})
	select {
	case ev := <-sub.C:
		if ev.Topic != "b" {
			t.Fatalf("got %s", ev.Topic)
		}
	case <-time.After(time.Second):
		t.Fatal("no event")
	}
	if RepoTopic("o", "r") != "repo:o/r" {
		t.Fatal(RepoTopic("o", "r"))
	}
}

func TestDropWhenFull(t *testing.T) {
	b := New()
	_, cancel := b.Subscribe("a")
	defer cancel()
	for i := 0; i < 40; i++ {
		b.Publish(Event{Topic: "a"})
	}
	_, _, dropped := b.Stats()
	if dropped != 24 {
		t.Fatalf("dropped = %d, want 24", dropped)
	}
}
