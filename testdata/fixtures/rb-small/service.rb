# Service layer exercising require_relative + inferred instance-method calls.

require_relative 'models'

MAX_DOGS = 10

def make_dog(name)
  Dog.new(name, "lab")
end

def describe(name)
  d = Dog.new(name, "lab")
  sound = d.speak
  d.breed
  sound
end
