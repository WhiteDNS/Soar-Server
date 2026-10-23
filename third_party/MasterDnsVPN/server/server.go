// Package server exposes the upstream wire protocol as an embedded DNS engine.
// WhiteDNS integration; upstream authorship and MIT license remain in LICENSE.
package server

import "masterdnsvpn-go/internal/udpserver"

type Config = udpserver.EmbeddedConfig
type Server = udpserver.EmbeddedServer

var New = udpserver.NewEmbedded
