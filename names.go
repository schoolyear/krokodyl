package main

import (
	"crypto/rand"
	"math/big"
)

// Friendly, human-readable instance names ("Brave Otter"). The raw OS
// hostname ("LAPTOP-24W12341") is unreadable and identical across two windows
// on one machine; a fresh random name per process makes every krokodyl window
// individually recognizable in the nearby list. Picked with crypto/rand so
// two near-simultaneous launches do not collide on a time seed.

var nameAdjectives = []string{
	"Brave", "Calm", "Clever", "Bold", "Bright", "Swift", "Quiet", "Gentle",
	"Lucky", "Merry", "Noble", "Proud", "Quick", "Sunny", "Witty", "Wise",
	"Eager", "Fair", "Keen", "Lively", "Mighty", "Nimble", "Plucky", "Sleek",
	"Snug", "Spry", "Sturdy", "Tidy", "Vivid", "Warm", "Zany", "Jolly",
}

var nameAnimals = []string{
	"Otter", "Falcon", "Badger", "Heron", "Lynx", "Marten", "Raven", "Stoat",
	"Wombat", "Ferret", "Gecko", "Hare", "Ibis", "Jackal", "Koala", "Lemur",
	"Mongoose", "Newt", "Ocelot", "Panda", "Quokka", "Rabbit", "Salmon", "Tapir",
	"Urchin", "Viper", "Walrus", "Yak", "Zebra", "Bison", "Crane", "Dingo",
}

// randomDeviceName returns an "Adjective Animal" name. On the vanishingly
// unlikely failure of the system RNG it falls back to the first words so the
// app still gets a usable name.
func randomDeviceName() string {
	return pick(nameAdjectives) + " " + pick(nameAnimals)
}

func pick(words []string) string {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(words))))
	if err != nil {
		return words[0]
	}
	return words[n.Int64()]
}
