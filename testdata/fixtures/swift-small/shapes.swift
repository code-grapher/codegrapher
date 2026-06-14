// shapes defines a Shape protocol, a Base/Circle class pair, a Point struct
// conforming to Shape via an extension, and a Kind enum.

import Foundation

/// Shape is conformed to by geometric figures.
protocol Shape {
    func area() -> Double
    func label() -> String
}

/// Base is the common superclass for shapes.
class Base {
    var name: String

    init(name: String) {
        self.name = name
    }

    func label() -> String {
        return name
    }
}

/// Circle is a circle with a radius, conforming to Shape.
class Circle: Base, Shape {
    let radius: Double

    init(radius: Double) {
        self.radius = radius
        super.init(name: "circle")
    }

    func area() -> Double {
        return PI * radius * radius
    }

    override func label() -> String {
        return "circle"
    }
}

/// Point is a 2D point.
struct Point {
    var x: Double
    var y: Double
}

/// Point conforms to Shape via an extension.
extension Point: Shape {
    func area() -> Double {
        return x * y
    }

    func label() -> String {
        return "point"
    }
}

/// Kind enumerates the supported shape families.
enum Kind {
    case round
    case polygon
    case custom
}

let PI = 3.14159
