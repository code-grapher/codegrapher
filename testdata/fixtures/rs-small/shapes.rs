// shapes defines a Shape trait and a Circle struct implementing it.
pub mod shapes {
    /// Shape is implemented by geometric figures.
    pub trait Shape {
        fn area(&self) -> f64;
        fn label(&self) -> String;
    }

    /// Circle is a circle with a radius.
    pub struct Circle {
        pub radius: f64,
    }

    impl Circle {
        /// new builds a Circle of the given radius.
        pub fn new(radius: f64) -> Circle {
            Circle { radius }
        }

        pub fn diameter(&self) -> f64 {
            self.radius * 2.0
        }
    }

    impl Shape for Circle {
        fn area(&self) -> f64 {
            3.14159 * self.radius * self.radius
        }

        fn label(&self) -> String {
            String::from("circle")
        }
    }

    /// Kind enumerates the supported shape families.
    pub enum Kind {
        Round,
        Polygon,
        Custom(u8),
    }

    pub const PI: f64 = 3.14159;
}
