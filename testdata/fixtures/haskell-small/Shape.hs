-- Domain type class + a concrete shape for the haskell-small fixture.
module Geometry where

class Shape a where
  area :: a -> Double

data Circle = Circle Double

label :: Circle -> String
label (Circle _) = "circle"

instance Shape Circle where
  area (Circle r) = r * r * 3
