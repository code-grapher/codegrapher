module Geometry

using Shapes
import Shapes: area

scale = 2.0

function describe()
    c = Circle(1.0)
    a = area(c)
    b = Shapes.area(c)
    return a + b
end

end
