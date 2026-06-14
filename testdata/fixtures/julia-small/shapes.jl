module Shapes

abstract type Shape end

struct Circle <: Shape
    r::Float64
end

struct Rectangle <: Shape
    w::Float64
    h::Float64
end

area(c::Circle) = 3.14159 * c.r * c.r

function area(rect::Rectangle)
    return rect.w * rect.h
end

const ORIGIN = 0.0

end
