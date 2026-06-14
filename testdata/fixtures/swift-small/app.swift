// app constructs the shapes and calls their methods across files.

/// run builds a Circle and a Point and reports their areas via Shape.
func run() -> Double {
    let c = Circle(radius: 2.0)
    let a = c.area()
    let l = c.label()
    let p = Point(x: 3.0, y: 4.0)
    let b = p.area()
    return a + b
}
