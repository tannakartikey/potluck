// Package handle generates friendly, anonymous display names — an
// adjective-noun-number handle in the spirit of Docker/Heroku. A contributor who
// registers without a --name still gets a distinct, readable identity on the
// public board, carrying no personal information.
package handle

import (
	"fmt"
	"math/rand"
)

// adjectives and nouns are intentionally playful; the pair plus a small number
// gives ~1M combinations, more than enough to keep board names readable and
// rarely-colliding. The contributor UUID, not this name, is the real identity.
var adjectives = []string{
	"brave", "sleepy", "grumpy", "sneaky", "wobbly", "jolly", "zesty", "fuzzy",
	"cosmic", "turbo", "salty", "spicy", "noble", "feral", "dapper", "plucky",
	"breezy", "cranky", "giddy", "snappy", "wonky", "perky", "mellow", "rowdy",
	"quirky", "soggy", "chunky", "nimble", "loopy", "groovy", "bouncy", "vivid",
}

var nouns = []string{
	"otter", "narwhal", "penguin", "wombat", "raccoon", "platypus", "lemur", "yak",
	"walrus", "gecko", "mongoose", "ferret", "badger", "axolotl", "capybara", "quokka",
	"pangolin", "tapir", "ocelot", "marmot", "puffin", "manatee", "okapi", "dingo",
	"hedgehog", "armadillo", "chinchilla", "meerkat", "numbat", "wallaby", "stoat", "lynx",
}

// Generate returns a random funny handle like "spicy-axolotl-482".
// math/rand's global source is auto-seeded on Go 1.20+, so each process yields a
// different handle without explicit seeding.
func Generate() string {
	adj := adjectives[rand.Intn(len(adjectives))]
	noun := nouns[rand.Intn(len(nouns))]
	return fmt.Sprintf("%s-%s-%d", adj, noun, rand.Intn(900)+100)
}
