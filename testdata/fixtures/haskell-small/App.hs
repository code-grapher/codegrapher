-- Application module exercising import + cross-module call resolution.
module App where

import Geometry

run :: Double -> Double
run radius = area (Circle radius)
