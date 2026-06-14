# Domain models for the rb-small fixture.

DEFAULT_SOUND = "..."

module Walkable
  def walk
    "walking"
  end
end

module Animals
  class Animal
    attr_reader :name

    def initialize(name)
      @name = name
    end

    def speak
      DEFAULT_SOUND
    end
  end

  class Dog < Animal
    include Walkable

    attr_accessor :breed

    def initialize(name, breed)
      super(name)
      @breed = breed
      @legs = 4
    end

    def speak
      "woof"
    end

    def self.create(name)
      Dog.new(name, "mutt")
    end
  end
end
