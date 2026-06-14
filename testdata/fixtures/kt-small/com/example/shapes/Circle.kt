package com.example.shapes

class Circle(name: String, private val radius: Double) : Base(name) {
    override fun area(): Double {
        return PI * radius * radius
    }

    companion object {
        const val PI = 3.14159

        fun unit(): Circle {
            return Circle("unit", 1.0)
        }
    }
}
