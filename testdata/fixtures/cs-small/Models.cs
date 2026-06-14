using System;

namespace CsSmall.Models
{
    public enum Color
    {
        Red,
        Green,
        Blue
    }

    public interface IGreeter
    {
        string Greet();
    }

    [Serializable]
    public abstract class Animal
    {
        public string Name { get; set; }

        public abstract string Speak();
    }

    public class Dog : Animal, IGreeter
    {
        private const int Legs = 4;

        public Color Coat { get; set; }

        public string Label => Name;

        public override string Speak()
        {
            return "woof";
        }

        public async Task<string> GreetAsync()
        {
            return Greet();
        }

        public string Greet()
        {
            return "hello";
        }
    }
}
