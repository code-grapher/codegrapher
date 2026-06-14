-- shape.lua — a small module table with functions and a method.
local Shape = {}

PI = 3.14159

-- Constructor-style factory (a plain call, not a real constructor).
function Shape.new(width, height)
  local self = { width = width, height = height }
  return self
end

-- Method with implicit self.
function Shape:area()
  return self.width * self.height
end

-- Dotted function that calls a sibling method-style function.
function Shape.label(s)
  return "area=" .. tostring(Shape.area(s))
end

return Shape
