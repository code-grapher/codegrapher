module Shapes

let area w h = w * h

type Circle(radius: float) =
    member _.Radius = radius
    member this.Area() = area this.Radius this.Radius
